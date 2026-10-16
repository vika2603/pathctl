// Package shell renders the code that `pathctl init` prints.
package shell

import (
	"fmt"
	"strings"
)

// Supported lists the shells init can render for.
var Supported = []string{"zsh", "bash"}

// Render returns code that exports PATH and PATHCTL_DIRS and defines a
// pathctl function. The function runs the binary at self and then brings the
// current shell up to date: it re-runs init after changes to the directory
// list, and clears the command hash after changes to overrides, since those
// leave PATH as it is.
func Render(shell, self string, path, managed []string) (string, error) {
	var rehash string
	switch shell {
	case "zsh":
		rehash = "rehash"
	case "bash":
		rehash = "hash -r"
	default:
		return "", fmt.Errorf("unsupported shell %q (supported: %s)", shell, strings.Join(Supported, ", "))
	}
	bin := Quote(self)
	var b strings.Builder
	fmt.Fprintf(&b, "export PATH=%s\n", Quote(strings.Join(path, ":")))
	fmt.Fprintf(&b, "export PATHCTL_DIRS=%s\n", Quote(strings.Join(managed, ":")))
	fmt.Fprintf(&b, "pathctl() {\n")
	fmt.Fprintf(&b, "  %s \"$@\" || return\n", bin)
	fmt.Fprintf(&b, "  case \"$1\" in\n")
	fmt.Fprintf(&b, "    add|rm|remove|edit) eval \"$(%s init %s)\" ;;\n", bin, shell)
	fmt.Fprintf(&b, "    use|unuse) %s ;;\n", rehash)
	fmt.Fprintf(&b, "  esac\n")
	fmt.Fprintf(&b, "}\n")
	return b.String(), nil
}

// Quote returns s as a single-quoted shell word.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
