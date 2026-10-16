package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const sample = `# my tools
dir ~/.local/bin
dir	$HOME/go/bin

# pinned commands
use python3 /opt/homebrew/bin/python3
`

func getenv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func mustParse(t *testing.T, text string) *File {
	t.Helper()
	f, err := Parse("config", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestParse(t *testing.T) {
	f := mustParse(t, sample)
	if got, want := f.Dirs(), []string{"~/.local/bin", "$HOME/go/bin"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Dirs() = %q, want %q", got, want)
	}
	if got, want := f.Overrides(), []Override{{"python3", "/opt/homebrew/bin/python3"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Overrides() = %q, want %q", got, want)
	}
	if got := string(f.Bytes()); got != sample {
		t.Errorf("Bytes() changed an unedited file:\n%s", got)
	}
}

func TestParseErrorsNameTheLine(t *testing.T) {
	cases := map[string]string{
		"dir ~/a\nadd ~/b\n":                "config:2: unknown directive \"add\"",
		"dir\n":                             "config:1: dir needs a directory",
		"use python3\n":                     "config:1: use needs a command name and a file",
		"use a/b /x\n":                      "config:1: invalid command name \"a/b\"",
		"use go /a/go\n# x\nuse go /b/go\n": "config:3: \"go\" is already set on line 1",
		"use a\u00a0b /x\n":                 "config:1: path \"b /x\" must be absolute or start with ~ or $",
		"dir relative/bin\n":                "config:1: path \"relative/bin\" must be absolute or start with ~ or $",
		"dir ~/${FOO/bin\n":                 "config:1: unclosed ${ in \"~/${FOO/bin\"",
		"dir $1/bin\n":                      "config:1: invalid variable reference in \"$1/bin\"",
		"use x /a\x7fb\n":                   `config:1: path "/a\x7fb" contains a control character`,
	}
	for text, want := range cases {
		if _, err := Parse("config", []byte(text)); err == nil || err.Error() != want {
			t.Errorf("Parse(%q) error = %v, want %q", text, err, want)
		}
	}
}

func TestEditsKeepCommentsAndGrouping(t *testing.T) {
	f := mustParse(t, sample)
	f.AddDir("~/bin")
	f.SetOverride("python3", "~/py/bin/python3")
	f.SetOverride("make", "/opt/homebrew/bin/gmake")
	want := `# my tools
dir ~/.local/bin
dir	$HOME/go/bin
dir ~/bin

# pinned commands
use python3 ~/py/bin/python3
use make /opt/homebrew/bin/gmake
`
	if got := string(f.Bytes()); got != want {
		t.Fatalf("after edits:\n%s\nwant:\n%s", got, want)
	}

	env := getenv(map[string]string{"HOME": "/home/u"})
	if !f.RemoveDir("/home/u/go/bin", func(p string) string { return Expand(p, env) }) {
		t.Fatal("RemoveDir did not match $HOME/go/bin by its expansion")
	}
	if !f.RemoveOverride("make") || f.RemoveOverride("make") {
		t.Fatal("RemoveOverride should remove make exactly once")
	}
	if strings.Contains(string(f.Bytes()), "go/bin") || strings.Contains(string(f.Bytes()), "gmake") {
		t.Fatalf("removed lines remain:\n%s", f.Bytes())
	}
}

func TestRemoveDirRemovesEveryMatch(t *testing.T) {
	f := mustParse(t, "dir ~/bin\ndir /home/u/bin\ndir /other\n")
	expand := func(p string) string { return Expand(p, getenv(map[string]string{"HOME": "/home/u"})) }
	if !f.RemoveDir("/home/u/bin/", expand) {
		t.Fatal("RemoveDir found nothing")
	}
	if got := string(f.Bytes()); got != "dir /other\n" {
		t.Fatalf("after RemoveDir: %q", got)
	}
}

func TestValidNameAndCheckPath(t *testing.T) {
	for _, name := range []string{"a\nb", "a\u00a0b", "a b", "a/b", ".", "..", ""} {
		if ValidName(name) == nil {
			t.Errorf("ValidName(%q) accepted", name)
		}
	}
	for _, path := range []string{"/tmp/x\nbogus", " /lead", "/trail ", "rel", "~user/x", "$", "${}"} {
		if CheckPath(path) == nil {
			t.Errorf("CheckPath(%q) accepted", path)
		}
	}
	for _, path := range []string{"/a b/c", "~", "~/x", "$HOME/x", "${XDG_DATA_HOME}/bin", "/a#b"} {
		if err := CheckPath(path); err != nil {
			t.Errorf("CheckPath(%q) = %v", path, err)
		}
	}
}

func TestAddToEmptyFile(t *testing.T) {
	f := mustParse(t, "# only a comment\n")
	f.SetOverride("go", "/usr/local/go/bin/go")
	f.AddDir("/opt/bin")
	if got, want := string(f.Bytes()), "# only a comment\nuse go /usr/local/go/bin/go\ndir /opt/bin\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSaveThroughSymlinkKeepsLinkAndMode(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles", "config")
	os.MkdirAll(filepath.Dir(real), 0o755)
	os.WriteFile(real, []byte("dir /a\n"), 0o600)
	link := filepath.Join(dir, "xdg", "pathctl", "config")
	os.MkdirAll(filepath.Dir(link), 0o755)
	os.Symlink(real, link)

	f, err := Load(link)
	if err != nil {
		t.Fatal(err)
	}
	f.AddDir("/b")
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config is no longer a symlink: %v", err)
	}
	data, _ := os.ReadFile(real)
	if string(data) != "dir /a\ndir /b\n" {
		t.Fatalf("target = %q", data)
	}
	if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestSaveThroughDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dots", "pathctl-config")
	link := filepath.Join(dir, "config")
	os.Symlink("dots/pathctl-config", link)

	f, _ := Load(link)
	f.AddDir("/a")
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("config is no longer a symlink")
	}
	if data, _ := os.ReadFile(real); string(data) != "dir /a\n" {
		t.Fatalf("link target = %q", data)
	}
}

func TestLoadMissingAndSaveCreates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "config")
	f, err := Load(path)
	if err != nil || len(f.Lines) != 0 {
		t.Fatalf("Load(missing) = %v, %v", f, err)
	}
	f.AddDir("/a")
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "dir /a\n" {
		t.Fatalf("saved %q", data)
	}
}

func TestExpand(t *testing.T) {
	env := getenv(map[string]string{"HOME": "/home/u", "GOBIN": "/go/bin", "REL": "rel"})
	cases := map[string]string{
		"~":              "/home/u",
		"~/bin":          "/home/u/bin",
		"$HOME/x":        "/home/u/x",
		"${GOBIN}":       "/go/bin",
		"/plain":         "/plain",
		"$UNSET/bin":     "",
		"~other/bin":     "",
		"/a/${UNSET}/b":  "",
		"$GOBIN/../tool": "/go/tool",
		"~/bin/":         "/home/u/bin",
		"~/${GOBIN":      "",
		"$REL/bin":       "",
	}
	for in, want := range cases {
		if got := Expand(in, env); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Expand("~/bin", getenv(nil)); got != "" {
		t.Errorf("Expand without HOME = %q, want empty", got)
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct{ in, cwd, want string }{
		{".", "/home/u/proj", "~/proj"},
		{"bin", "/home/u", "~/bin"},
		{"~/x/../y", "/", "~/y"},
		{"/home/u", "/", "~"},
		{"/home/u2/bin", "/", "/home/u2/bin"},
		{"/opt//bin/", "/", "/opt/bin"},
		{"$GOPATH/bin", "/home/u", "$GOPATH/bin"},
	}
	for _, c := range cases {
		if got := Normalize(c.in, c.cwd, "/home/u"); got != c.want {
			t.Errorf("Normalize(%q, %q) = %q, want %q", c.in, c.cwd, got, c.want)
		}
	}
}
