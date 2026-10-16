package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type harness struct {
	t      *testing.T
	home   string
	env    map[string]string
	editor func(editor, path string) error
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	return &harness{t: t, home: home, env: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin"}}
}

func (h *harness) run(cwd string, args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	return run(args, &app{
		stdin:     strings.NewReader(""),
		stdout:    &h.stdout,
		stderr:    &h.stderr,
		getenv:    func(k string) string { return h.env[k] },
		cwd:       cwd,
		self:      "/opt/pathctl/bin/pathctl",
		runEditor: func(editor, path string) error { return h.editor(editor, path) },
	})
}

func (h *harness) must(args ...string) string {
	h.t.Helper()
	if code := h.run(h.home, args...); code != 0 {
		h.t.Fatalf("pathctl %s: exit %d\n%s", strings.Join(args, " "), code, h.stderr.String())
	}
	return h.stdout.String()
}

func (h *harness) config() string {
	data, _ := os.ReadFile(filepath.Join(h.home, ".config", "pathctl", "config"))
	return string(data)
}

func (h *harness) mkdir(rel string) string {
	p := filepath.Join(h.home, rel)
	os.MkdirAll(p, 0o755)
	return p
}

func (h *harness) exe(rel string) string {
	p := filepath.Join(h.home, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755)
	return p
}

func (h *harness) overrideDir() string {
	return filepath.Join(h.home, ".local", "share", "pathctl", "bin")
}

func TestAddAndRemove(t *testing.T) {
	h := newHarness(t)
	h.mkdir("bin")
	h.must("add", "bin")
	if out := h.must("add", h.home+"/bin"); !strings.Contains(out, "already added") {
		t.Errorf("second add: %q", out)
	}
	if code := h.run(h.home, "add", "later"); code != 0 || !strings.Contains(h.stderr.String(), "does not exist yet") {
		t.Errorf("add of a missing dir: exit %d, stderr %q", code, h.stderr.String())
	}
	if got := h.config(); got != "dir ~/bin\ndir ~/later\n" {
		t.Fatalf("config = %q", got)
	}

	if code := h.run(h.home, "rm", "bin", "nope"); code != 1 || h.config() != "dir ~/bin\ndir ~/later\n" {
		t.Fatalf("rm with an unknown dir: exit %d, config %q", code, h.config())
	}
	h.must("rm", "~/later")
	if got := h.config(); got != "dir ~/bin\n" {
		t.Fatalf("config = %q", got)
	}
}

func TestUseAndUnuse(t *testing.T) {
	h := newHarness(t)
	target := h.exe("tools/python3")
	os.WriteFile(filepath.Join(h.home, "plain"), nil, 0o644)

	if code := h.run(h.home, "use", "python3", "plain"); code != 1 {
		t.Errorf("use of a non-executable file: exit %d", code)
	}
	if code := h.run(h.home, "use", "a/b", target); code != 2 {
		t.Errorf("use with an invalid name: exit %d", code)
	}
	h.must("use", "python3", target)
	link := filepath.Join(h.overrideDir(), "python3")
	if got, _ := os.Readlink(link); got != target {
		t.Fatalf("link -> %q, want %q", got, target)
	}
	if got := h.config(); got != "use python3 ~/tools/python3\n" {
		t.Fatalf("config = %q", got)
	}

	h.must("unuse", "python3")
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("link remains after unuse")
	}
	if code := h.run(h.home, "unuse", "python3"); code != 1 {
		t.Errorf("unuse of an unknown name: exit %d", code)
	}
}

func TestList(t *testing.T) {
	h := newHarness(t)
	h.mkdir("bin")
	h.run(h.home, "add", "bin", "gone")
	h.must("use", "tool", h.exe("t/tool"))
	os.Remove(filepath.Join(h.home, "t", "tool"))
	want := "dir  ~/bin\ndir  ~/gone  (missing)\nuse  tool -> ~/t/tool  (missing)\n"
	if got := h.must("ls"); got != want {
		t.Fatalf("ls:\n%s\nwant:\n%s", got, want)
	}
}

func TestInit(t *testing.T) {
	h := newHarness(t)
	bin := h.mkdir("bin")
	h.must("add", "bin")
	h.env["PATH"] = "/usr/bin:" + bin + ":/bin"

	out := h.must("init", "zsh")
	wantPath := "export PATH='" + h.overrideDir() + ":/usr/bin:" + bin + ":/bin'\n"
	if !strings.HasPrefix(out, wantPath) {
		t.Fatalf("init output starts with:\n%s\nwant:\n%s", strings.SplitN(out, "\n", 2)[0], wantPath)
	}
	if !strings.Contains(out, "'/opt/pathctl/bin/pathctl' \"$@\"") {
		t.Errorf("init does not call the binary by its path:\n%s", out)
	}
	if code := h.run(h.home, "init", "fish"); code != 2 || h.stdout.Len() != 0 {
		t.Errorf("init fish: exit %d, stdout %q", code, h.stdout.String())
	}
}

func TestInitRepairsLinksAfterHandEdits(t *testing.T) {
	h := newHarness(t)
	target := h.exe("t/tool")
	os.MkdirAll(filepath.Join(h.home, ".config", "pathctl"), 0o755)
	os.WriteFile(filepath.Join(h.home, ".config", "pathctl", "config"), []byte("use tool ~/t/tool\n"), 0o644)
	h.must("init", "bash")
	if got, _ := os.Readlink(filepath.Join(h.overrideDir(), "tool")); got != target {
		t.Fatalf("init did not create the link: %q", got)
	}
}

func TestDoctor(t *testing.T) {
	h := newHarness(t)
	mine := h.mkdir("bin")
	sys := h.mkdir("sys")
	h.exe("bin/make")
	h.exe("sys/make")
	h.must("add", "bin")
	h.env["PATH"] = h.overrideDir() + ":" + sys + ":" + mine

	if code := h.run(h.home, "doctor"); code != 1 || !strings.Contains(h.stdout.String(), "make: ~/sys/make runs; also found:\n  ~/bin/make\n  choose one with: pathctl use make <file>\n") {
		t.Fatalf("doctor with a conflict: exit %d\n%s", code, h.stdout.String())
	}
	h.must("use", "make", "bin/make")
	if out := h.must("doctor"); out != "no problems found\n" {
		t.Fatalf("doctor after use:\n%s", out)
	}
}

func TestEdit(t *testing.T) {
	h := newHarness(t)
	target := h.exe("t/tool")
	h.editor = func(_, path string) error { return os.WriteFile(path, []byte("use tool ~/t/tool\n"), 0o644) }
	h.must("edit")
	if got, _ := os.Readlink(filepath.Join(h.overrideDir(), "tool")); got != target {
		t.Fatalf("edit did not sync links: %q", got)
	}

	h.editor = func(_, path string) error { return os.WriteFile(path, []byte("dir ~/a\nbogus\n"), 0o644) }
	if code := h.run(h.home, "edit"); code != 1 || !strings.Contains(h.stderr.String(), "config:2: unknown directive") {
		t.Fatalf("edit with an invalid file: exit %d, stderr %q", code, h.stderr.String())
	}
}

func TestUsageErrors(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{}, {"bogus"}, {"add"}, {"add", "--first", "x"}, {"use", "x"}, {"init"}, {"unuse", "--help"}} {
		if code := h.run(h.home, args...); code != 2 {
			t.Errorf("pathctl %v: exit %d, want 2", args, code)
		}
	}
}

func TestRejectsValuesTheFileCannotHold(t *testing.T) {
	h := newHarness(t)
	tool := h.exe("t/tool")
	for _, args := range [][]string{
		{"use", "a\nb", tool},
		{"use", "a\u00a0b", tool},
		{"add", "/tmp/x\nbogus"},
		{"add", "/tmp/trailing "},
	} {
		if code := h.run(h.home, args...); code != 2 {
			t.Errorf("pathctl %q: exit %d, want 2", args, code)
		}
	}
	if got := h.config(); got != "" {
		t.Fatalf("config written: %q", got)
	}
	// Whatever was rejected, the file still loads.
	h.must("init", "zsh")
}

func TestUseKeepsRegularFilesInOverrideDir(t *testing.T) {
	h := newHarness(t)
	tool := h.exe("t/tool")
	mine := filepath.Join(h.overrideDir(), "mine")
	os.MkdirAll(h.overrideDir(), 0o755)
	os.WriteFile(mine, []byte("#!/bin/sh\n"), 0o755)

	if code := h.run(h.home, "use", "mine", tool); code != 1 {
		t.Fatalf("use over a regular file: exit %d", code)
	}
	if fi, err := os.Lstat(mine); err != nil || !fi.Mode().IsRegular() {
		t.Fatal("regular file was replaced")
	}
	if got := h.config(); got != "" {
		t.Fatalf("config written: %q", got)
	}
}

func TestInitWithoutPathKeepsSystemDirs(t *testing.T) {
	h := newHarness(t)
	delete(h.env, "PATH")
	out := h.must("init", "bash")
	want := "export PATH='" + h.overrideDir() + ":/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin'\n"
	if !strings.HasPrefix(out, want) {
		t.Fatalf("init without PATH:\n%s", out)
	}
}

// TestConcurrentInit starts many inits at once, as opening several terminals
// does, with links missing so every run has to create them.
func TestConcurrentInit(t *testing.T) {
	h := newHarness(t)
	var cfg strings.Builder
	for i := range 200 {
		h.exe(fmt.Sprintf("t/tool%d", i))
		fmt.Fprintf(&cfg, "use tool%d ~/t/tool%d\n", i, i)
	}
	os.MkdirAll(filepath.Join(h.home, ".config", "pathctl"), 0o755)
	os.WriteFile(filepath.Join(h.home, ".config", "pathctl", "config"), []byte(cfg.String()), 0o644)

	var wg sync.WaitGroup
	errs := make([]string, 8)
	for i := range errs {
		wg.Go(func() {
			var stdout, stderr bytes.Buffer
			code := run([]string{"init", "zsh"}, &app{
				stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr,
				getenv: func(k string) string { return h.env[k] }, cwd: h.home, self: "/x",
			})
			if code != 0 || stderr.Len() > 0 {
				errs[i] = fmt.Sprintf("exit %d: %s", code, stderr.String())
			}
		})
	}
	wg.Wait()
	for i, e := range errs {
		if e != "" {
			t.Errorf("init %d: %s", i, e)
		}
	}
	entries, _ := os.ReadDir(h.overrideDir())
	if len(entries) != 200 {
		t.Fatalf("override dir holds %d entries, want 200", len(entries))
	}
}

func TestSelfPath(t *testing.T) {
	look := func(name string) (string, error) {
		if name == "pathctl" {
			return "/opt/brew/bin/pathctl", nil
		}
		return "", errors.New("not found")
	}
	exe := func() (string, error) { return "/opt/brew/Cellar/pathctl/1.0/bin/pathctl", nil }
	cases := map[string]string{
		"/usr/local/bin/pathctl": "/usr/local/bin/pathctl",
		"pathctl":                "/opt/brew/bin/pathctl",
		"other":                  "/opt/brew/Cellar/pathctl/1.0/bin/pathctl",
	}
	for arg0, want := range cases {
		if got := selfPath(arg0, look, exe); got != want {
			t.Errorf("selfPath(%q) = %q, want %q", arg0, got, want)
		}
	}
}

func TestInitWithoutLockStillSetsPath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions do not apply to root")
	}
	h := newHarness(t)
	bin := h.mkdir("bin")
	h.must("add", "bin")
	data := h.mkdir("rodata")
	os.Chmod(data, 0o555)
	t.Cleanup(func() { os.Chmod(data, 0o755) })
	h.env["XDG_DATA_HOME"] = data

	if code := h.run(h.home, "init", "zsh"); code != 0 {
		t.Fatalf("init exit %d: %s", code, h.stderr.String())
	}
	want := "export PATH='" + filepath.Join(data, "pathctl", "bin") + ":/usr/bin:/bin:" + bin + "'\n"
	if !strings.HasPrefix(h.stdout.String(), want) || !strings.Contains(h.stderr.String(), "override links not updated") {
		t.Fatalf("stdout:\n%s\nstderr:\n%s", h.stdout.String(), h.stderr.String())
	}
	// Commands that change the configuration still need the lock.
	if code := h.run(h.home, "add", "other"); code != 1 {
		t.Fatalf("add without the lock: exit %d", code)
	}
}

func TestVariablePaths(t *testing.T) {
	h := newHarness(t)
	gopath := h.mkdir("go")
	h.mkdir("go/bin")
	h.env["GOPATH"] = gopath

	h.must("add", "$GOPATH/bin")
	if got := h.config(); got != "dir $GOPATH/bin\n" {
		t.Fatalf("config = %q", got)
	}
	if out := h.must("add", "~/go/bin"); !strings.Contains(out, "already added") {
		t.Fatalf("same directory written differently was added again: %q", out)
	}
	// With the variable unset, the entry can still be removed as written.
	delete(h.env, "GOPATH")
	h.must("rm", "$GOPATH/bin")
	if got := h.config(); got != "" {
		t.Fatalf("config = %q", got)
	}
}

func TestDoctorReportsRegularFileInOverrideDir(t *testing.T) {
	h := newHarness(t)
	h.exe("t/tool")
	os.MkdirAll(h.overrideDir(), 0o755)
	os.WriteFile(filepath.Join(h.overrideDir(), "tool"), []byte("#!/bin/sh\n"), 0o755)
	os.MkdirAll(filepath.Join(h.home, ".config", "pathctl"), 0o755)
	os.WriteFile(filepath.Join(h.home, ".config", "pathctl", "config"), []byte("use tool ~/t/tool\n"), 0o644)
	h.env["PATH"] = h.overrideDir() + ":/usr/bin"

	if code := h.run(h.home, "doctor"); code != 1 || !strings.Contains(h.stdout.String(), "tool: ~/.local/share/pathctl/bin/tool is a regular file") {
		t.Fatalf("doctor: exit %d\n%s", code, h.stdout.String())
	}
}
