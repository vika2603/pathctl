package shell

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderRejectsUnknownShell(t *testing.T) {
	if _, err := Render("fish", "/bin/pathctl", nil, nil); err == nil {
		t.Fatal("expected an error for fish")
	}
}

func TestQuote(t *testing.T) {
	if got, want := Quote("/a b/it's"), `'/a b/it'\''s'`; got != want {
		t.Fatalf("Quote = %s, want %s", got, want)
	}
}

// TestInRealShells evaluates the rendered code and exercises the pathctl
// function against a stub binary.
func TestInRealShells(t *testing.T) {
	for _, sh := range Supported {
		t.Run(sh, func(t *testing.T) {
			// macOS ships bash 3.2 as /bin/bash; prefer it so the oldest
			// supported bash is the one tested there.
			bin := "/bin/" + sh
			if _, err := os.Stat(bin); err != nil {
				if bin, err = exec.LookPath(sh); err != nil {
					t.Skipf("%s not installed", sh)
				}
			}
			dir := t.TempDir()
			next, _ := Render(sh, "unused", []string{"/o", "/usr/bin", "/bin", "/new"}, []string{"/new"})
			os.WriteFile(filepath.Join(dir, "next"), []byte(next), 0o644)
			stub := filepath.Join(dir, "it's pathctl")
			os.WriteFile(stub, []byte(`#!/bin/sh
case "$1" in
  init) cat "$(dirname "$0")/next" ;;
  fail) exit 3 ;;
esac
`), 0o755)

			first, err := Render(sh, stub, []string{"/o", "/usr/bin", "/bin"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			script := first + `
echo "1 $PATH|$PATHCTL_DIRS"
pathctl fail; echo "2 status=$? $PATH"
pathctl use x y; echo "3 status=$?"
pathctl add /new; echo "4 $PATH|$PATHCTL_DIRS"
`
			cmd := exec.Command(bin, "-c", script)
			cmd.Env = []string{"HOME=" + dir}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", sh, err, out)
			}
			want := strings.Join([]string{
				"1 /o:/usr/bin:/bin|",
				"2 status=3 /o:/usr/bin:/bin",
				"3 status=0",
				"4 /o:/usr/bin:/bin:/new|/new",
			}, "\n")
			if got := strings.TrimSpace(string(out)); got != want {
				t.Fatalf("output:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}
