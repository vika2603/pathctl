// Command pathctl keeps PATH predictable: directories are only ever appended,
// and commands that exist in more than one place are decided explicitly.
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vika2603/pathctl/internal/config"
	"github.com/vika2603/pathctl/internal/doctor"
	"github.com/vika2603/pathctl/internal/links"
	"github.com/vika2603/pathctl/internal/lock"
	"github.com/vika2603/pathctl/internal/pathenv"
	"github.com/vika2603/pathctl/internal/shell"
)

// Version is set at build time.
var Version = "dev"

const usage = `pathctl keeps PATH predictable.

Directories are only ever appended to PATH, so adding one never changes which
file an existing command runs. When a command exists in more than one place,
choose the file with 'use'.

Usage:
  pathctl add <dir>...         Append directories to PATH
  pathctl rm <dir>...          Remove directories
  pathctl use <name> <file>    Run <file> for command <name>
  pathctl unuse <name>         Remove the choice for <name>
  pathctl ls                   Show the configuration
  pathctl doctor               Report conflicts and choices that do not take effect
  pathctl edit                 Open the configuration in $VISUAL or $EDITOR
  pathctl init <zsh|bash>      Print shell code: eval "$(pathctl init zsh)"
  pathctl version              Print the version

Files:
  $XDG_CONFIG_HOME/pathctl/config   configuration (default ~/.config/pathctl/config)
  $XDG_DATA_HOME/pathctl/bin        links made by 'use' (default ~/.local/share/pathctl/bin)
`

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pathctl: %v\n", err)
		os.Exit(1)
	}
	os.Exit(run(os.Args[1:], &app{
		stdin:     os.Stdin,
		stdout:    os.Stdout,
		stderr:    os.Stderr,
		getenv:    os.Getenv,
		cwd:       cwd,
		self:      selfPath(os.Args[0], exec.LookPath, os.Executable),
		runEditor: runEditor,
	}))
}

// selfPath returns the path init embeds to call this binary again: the path it
// was started with, or the PATH entry that found it, without resolving
// symlinks. A stable symlink such as a package manager's bin entry therefore
// keeps working after an upgrade removes the old version.
func selfPath(arg0 string, lookPath func(string) (string, error), executable func() (string, error)) string {
	p := arg0
	if !strings.Contains(arg0, "/") {
		if found, err := lookPath(arg0); err == nil {
			p = found
		} else {
			p = ""
		}
	}
	if p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
	}
	if exe, err := executable(); err == nil {
		return exe
	}
	return "pathctl"
}

func runEditor(editor, path string) error {
	// The editor may carry arguments; the path is a positional so it is
	// neither split nor expanded.
	cmd := exec.Command("sh", "-c", editor+` "$1"`, "sh", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

type app struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	getenv         func(string) string
	cwd            string
	self           string
	runEditor      func(editor, path string) error
}

type usageError string

func (e usageError) Error() string { return string(e) }

// errProblems reports that doctor found problems it has already printed.
var errProblems = errors.New("problems found")

func run(args []string, a *app) int {
	if len(args) == 0 {
		fmt.Fprint(a.stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "add":
		err = a.add(rest)
	case "rm", "remove":
		err = a.remove(rest)
	case "use":
		err = a.use(rest)
	case "unuse":
		err = a.unuse(rest)
	case "ls", "list":
		err = a.list(rest)
	case "doctor":
		err = a.doctor(rest)
	case "edit":
		err = a.edit(rest)
	case "init":
		err = a.init(rest)
	case "version", "--version":
		fmt.Fprintf(a.stdout, "pathctl %s\n", Version)
	case "help", "-h", "--help":
		fmt.Fprint(a.stdout, usage)
	default:
		err = usageError(fmt.Sprintf("unknown command %q", cmd))
	}

	var ue usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ue):
		fmt.Fprintf(a.stderr, "pathctl: %s\nRun 'pathctl help' for usage.\n", ue)
		return 2
	case errors.Is(err, errProblems):
		return 1
	default:
		fmt.Fprintf(a.stderr, "pathctl: %v\n", err)
		return 1
	}
}

func (a *app) home() string { return a.getenv("HOME") }

func (a *app) xdg(name, fallback string) string {
	if dir := a.getenv(name); filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(a.home(), fallback)
}

func (a *app) configPath() string {
	return filepath.Join(a.xdg("XDG_CONFIG_HOME", ".config"), "pathctl", "config")
}

func (a *app) overrideDir() string {
	return filepath.Join(a.xdg("XDG_DATA_HOME", ".local/share"), "pathctl", "bin")
}

// locked runs fn while holding the lock that serializes changes to the
// configuration and the override directory.
func (a *app) locked(fn func() error) error {
	release, err := a.lock()
	if err != nil {
		return err
	}
	defer release()
	return fn()
}

func (a *app) lock() (func(), error) {
	if a.home() == "" {
		return nil, errors.New("HOME is not set")
	}
	release, err := lock.Acquire(filepath.Join(filepath.Dir(a.overrideDir()), "lock"))
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	return release, nil
}

// currentPath returns PATH from the environment. A shell started without PATH
// uses a built-in default it does not export, so pathctl falls back to the
// usual system directories rather than dropping them.
func (a *app) currentPath() []string {
	if p := a.getenv("PATH"); p != "" {
		return filepath.SplitList(p)
	}
	return []string{"/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}
}

func (a *app) expand(path string) string { return config.Expand(path, a.getenv) }

func (a *app) normalize(path string) string { return config.Normalize(path, a.cwd, a.home()) }

// short writes a path under the home directory as ~/... for display.
func (a *app) short(path string) string { return config.Normalize(path, "/", a.home()) }

func (a *app) load() (*config.File, error) {
	if a.home() == "" {
		return nil, errors.New("HOME is not set")
	}
	return config.Load(a.configPath())
}

func noOptions(args []string) error {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") && arg != "-" {
			return usageError(fmt.Sprintf("unknown option %q", arg))
		}
	}
	return nil
}

// existingDirs returns the configured directories that exist, expanded.
func (a *app) existingDirs(f *config.File) []string {
	var dirs []string
	for _, d := range f.Dirs() {
		if p := a.expand(d); p != "" && config.IsDir(p) {
			dirs = append(dirs, p)
		}
	}
	return dirs
}

// normalizeArg turns a command-line path into its stored form and checks that
// the file can hold it.
func (a *app) normalizeArg(arg string) (string, error) {
	p := a.normalize(arg)
	if err := config.CheckPath(p); err != nil {
		return "", usageError(err.Error())
	}
	return p, nil
}

func (a *app) syncLinks(f *config.File) error {
	var want []links.Link
	for _, o := range f.Overrides() {
		if target := a.expand(o.Path); target != "" {
			want = append(want, links.Link{Name: o.Name, Target: target})
		}
	}
	return links.Sync(a.overrideDir(), want)
}

func (a *app) add(args []string) error {
	if len(args) == 0 {
		return usageError("add needs at least one directory")
	}
	if err := noOptions(args); err != nil {
		return err
	}
	var dirs []string
	for _, arg := range args {
		dir, err := a.normalizeArg(arg)
		if err != nil {
			return err
		}
		dirs = append(dirs, dir)
	}
	return a.locked(func() error { return a.addLocked(dirs) })
}

func (a *app) addLocked(dirs []string) error {
	f, err := a.load()
	if err != nil {
		return err
	}
	changed := false
	for _, dir := range dirs {
		if f.HasDir(dir, a.expand) {
			fmt.Fprintf(a.stdout, "%s is already added\n", dir)
			continue
		}
		f.AddDir(dir)
		changed = true
		fmt.Fprintf(a.stdout, "added %s\n", dir)
		if !config.IsDir(a.expand(dir)) {
			fmt.Fprintf(a.stderr, "pathctl: %s does not exist yet; it goes on PATH once it does\n", dir)
		}
	}
	if !changed {
		return nil
	}
	return f.Save()
}

func (a *app) remove(args []string) error {
	if len(args) == 0 {
		return usageError("rm needs at least one directory")
	}
	if err := noOptions(args); err != nil {
		return err
	}
	return a.locked(func() error {
		f, err := a.load()
		if err != nil {
			return err
		}
		var missing []string
		for _, arg := range args {
			if !f.HasDir(a.normalize(arg), a.expand) {
				missing = append(missing, arg)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("not in the configuration: %s", strings.Join(missing, ", "))
		}
		for _, arg := range args {
			dir := a.normalize(arg)
			if f.RemoveDir(dir, a.expand) {
				fmt.Fprintf(a.stdout, "removed %s\n", dir)
			}
		}
		return f.Save()
	})
}

func (a *app) use(args []string) error {
	if len(args) != 2 {
		return usageError("use needs a command name and a file")
	}
	if err := noOptions(args); err != nil {
		return err
	}
	name := args[0]
	if err := config.ValidName(name); err != nil {
		return usageError(err.Error())
	}
	file, err := a.normalizeArg(args[1])
	if err != nil {
		return err
	}
	target := a.expand(file)
	if !config.IsExecutable(target) {
		return fmt.Errorf("%s is not an executable file", file)
	}
	if filepath.Dir(target) == a.overrideDir() {
		return fmt.Errorf("%s is inside the override directory", file)
	}
	return a.locked(func() error {
		if err := links.Occupied(a.overrideDir(), name); err != nil {
			return err
		}
		f, err := a.load()
		if err != nil {
			return err
		}
		f.SetOverride(name, file)
		if err := f.Save(); err != nil {
			return err
		}
		if err := a.syncLinks(f); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "%s -> %s\n", name, file)
		return nil
	})
}

func (a *app) unuse(args []string) error {
	if len(args) != 1 {
		return usageError("unuse needs a command name")
	}
	if err := noOptions(args); err != nil {
		return err
	}
	return a.locked(func() error {
		f, err := a.load()
		if err != nil {
			return err
		}
		if !f.RemoveOverride(args[0]) {
			return fmt.Errorf("no file is chosen for %q", args[0])
		}
		if err := f.Save(); err != nil {
			return err
		}
		if err := a.syncLinks(f); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "removed the choice for %s\n", args[0])
		return nil
	})
}

func (a *app) list(args []string) error {
	if len(args) > 0 {
		return usageError("ls takes no arguments")
	}
	f, err := a.load()
	if err != nil {
		return err
	}
	dirs, overrides := f.Dirs(), f.Overrides()
	if len(dirs) == 0 && len(overrides) == 0 {
		fmt.Fprintf(a.stdout, "nothing configured in %s\n", a.short(a.configPath()))
		return nil
	}
	for _, d := range dirs {
		note := ""
		if !config.IsDir(a.expand(d)) {
			note = "  (missing)"
		}
		fmt.Fprintf(a.stdout, "dir  %s%s\n", d, note)
	}
	for _, o := range overrides {
		note := ""
		if !config.IsExecutable(a.expand(o.Path)) {
			note = "  (missing)"
		}
		fmt.Fprintf(a.stdout, "use  %s -> %s%s\n", o.Name, o.Path, note)
	}
	return nil
}

func (a *app) doctor(args []string) error {
	if len(args) > 0 {
		return usageError("doctor takes no arguments")
	}
	f, err := a.load()
	if err != nil {
		return err
	}
	in := doctor.Input{
		Path:        a.currentPath(),
		OverrideDir: a.overrideDir(),
		Dirs:        a.existingDirs(f),
	}
	configured := map[string]string{}
	for _, o := range f.Overrides() {
		configured[o.Name] = o.Path
		in.Overrides = append(in.Overrides, doctor.Override{Name: o.Name, Target: a.expand(o.Path)})
	}
	problems := doctor.Check(in)
	if len(problems) == 0 {
		fmt.Fprintln(a.stdout, "no problems found")
		return nil
	}
	for _, p := range problems {
		a.printProblem(p, in, configured)
	}
	return errProblems
}

const reinit = "  open a new shell, or run: eval \"$(pathctl init zsh)\" (or bash)\n"

func (a *app) printProblem(p doctor.Problem, in doctor.Input, configured map[string]string) {
	w := a.stdout
	switch p.Kind {
	case doctor.NotOnPath:
		fmt.Fprintf(w, "%s is not on PATH, so choices made with 'use' do not take effect\n", a.short(p.Target))
		fmt.Fprint(w, reinit)
	case doctor.DirNotOnPath:
		fmt.Fprintf(w, "%s is configured but not on PATH\n", a.short(p.Target))
		fmt.Fprint(w, reinit)
	case doctor.Broken:
		fmt.Fprintf(w, "%s: the chosen file %s is missing, not executable, or cannot be resolved\n", p.Name, configured[p.Name])
	case doctor.Blocked:
		fmt.Fprintf(w, "%s: %s is a regular file, so the link for the chosen %s cannot be placed\n", p.Name, a.short(p.Target), configured[p.Name])
		fmt.Fprintf(w, "  move or remove it, then run: pathctl init zsh >/dev/null\n")
	case doctor.Stale:
		fmt.Fprintf(w, "%s: the link in %s does not point to %s\n", p.Name, a.short(in.OverrideDir), configured[p.Name])
		fmt.Fprint(w, reinit)
	case doctor.Shadowed:
		fmt.Fprintf(w, "%s: %s runs instead of the chosen %s\n", p.Name, a.short(p.Winner), configured[p.Name])
		fmt.Fprintf(w, "  %s comes before %s on PATH\n", a.short(filepath.Dir(p.Winner)), a.short(in.OverrideDir))
	case doctor.Conflict:
		fmt.Fprintf(w, "%s: %s runs; also found:\n", p.Name, a.short(p.Winner))
		for _, o := range p.Others {
			fmt.Fprintf(w, "  %s\n", a.short(o))
		}
		fmt.Fprintf(w, "  choose one with: pathctl use %s <file>\n", p.Name)
	}
}

func (a *app) edit(args []string) error {
	if len(args) > 0 {
		return usageError("edit takes no arguments")
	}
	if a.home() == "" {
		return errors.New("HOME is not set")
	}
	path := a.configPath()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			return err
		}
	}
	editor := a.getenv("VISUAL")
	if editor == "" {
		editor = a.getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	if err := a.runEditor(editor, path); err != nil {
		return fmt.Errorf("editor: %w", err)
	}
	return a.locked(func() error {
		f, err := a.load()
		if err != nil {
			return err
		}
		return a.syncLinks(f)
	})
}

func (a *app) init(args []string) error {
	if len(args) != 1 {
		return usageError("init needs a shell: " + strings.Join(shell.Supported, " or "))
	}
	if _, err := shell.Render(args[0], "", nil, nil); err != nil {
		return usageError(err.Error())
	}
	// Every shell runs init, so a problem with the lock or the links must not
	// cost it its PATH: report it, leave the links as they are, and carry on.
	release, err := a.lock()
	if err != nil {
		fmt.Fprintf(a.stderr, "pathctl: %v; override links not updated\n", err)
	} else {
		defer release()
	}
	f, err := a.load()
	if err != nil {
		return err
	}
	if release != nil {
		if err := a.syncLinks(f); err != nil {
			fmt.Fprintf(a.stderr, "pathctl: %v\n", err)
		}
	}
	path, managed := pathenv.Compute(
		a.currentPath(),
		a.overrideDir(),
		filepath.SplitList(a.getenv("PATHCTL_DIRS")),
		a.existingDirs(f),
	)
	code, err := shell.Render(args[0], a.self, path, managed)
	if err != nil {
		return err
	}
	fmt.Fprint(a.stdout, code)
	return nil
}
