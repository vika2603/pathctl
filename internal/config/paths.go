package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

var errUnset = errors.New("unset variable")

// CheckPath reports whether path can be stored in the file. It must be
// absolute or start with ~ or a variable, contain no control characters or
// surrounding white space, and use only $NAME and ${NAME} references.
func CheckPath(path string) error {
	switch {
	case path == "":
		return errors.New("empty path")
	case strings.IndexFunc(path, unicode.IsControl) >= 0:
		return fmt.Errorf("path %q contains a control character", path)
	case strings.TrimSpace(path) != path:
		return fmt.Errorf("path %q starts or ends with white space", path)
	case !rooted(path):
		return fmt.Errorf("path %q must be absolute or start with ~ or $", path)
	}
	_, err := substitute(path, func(string) string { return "x" })
	return err
}

func rooted(path string) bool {
	return filepath.IsAbs(path) || path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "$")
}

// Expand resolves a leading ~ and $NAME or ${NAME} references, and cleans the
// result. It returns "" when the path cannot be resolved: a referenced
// variable is unset or empty (substituting nothing would turn "$UNSET/bin"
// into "/bin"), the syntax is invalid, or the result is not absolute.
func Expand(path string, getenv func(string) string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home := getenv("HOME")
		if home == "" {
			return ""
		}
		path = home + path[1:]
	}
	out, err := substitute(path, getenv)
	if err != nil || !filepath.IsAbs(out) {
		return ""
	}
	return filepath.Clean(out)
}

func substitute(s string, getenv func(string) string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '$' {
			b.WriteByte(s[i])
			i++
			continue
		}
		i++
		var name string
		if i < len(s) && s[i] == '{' {
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				return "", fmt.Errorf("unclosed ${ in %q", s)
			}
			name = s[i+1 : i+end]
			i += end + 1
		} else {
			j := i
			for j < len(s) && isNameByte(s[j], j == i) {
				j++
			}
			name = s[i:j]
			i = j
		}
		if name == "" || !isName(name) {
			return "", fmt.Errorf("invalid variable reference in %q", s)
		}
		v := getenv(name)
		if v == "" {
			return "", errUnset
		}
		b.WriteString(v)
	}
	return b.String(), nil
}

func isNameByte(c byte, first bool) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || !first && c >= '0' && c <= '9'
}

func isName(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isNameByte(s[i], i == 0) {
			return false
		}
	}
	return true
}

// Normalize turns a path given on the command line into the form written to
// the file: absolute, cleaned, and with the home directory written as ~ so the
// file can be shared between machines. Symlinks are kept as given. A path
// starting with $ is kept as written, so it is expanded when used.
func Normalize(path, cwd, home string) string {
	if strings.HasPrefix(path, "$") {
		return path
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		path = home + path[1:]
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	switch {
	case home == "" || home == "/":
	case path == home:
		path = "~"
	case strings.HasPrefix(path, home+"/"):
		path = "~" + path[len(home):]
	}
	return path
}

// IsDir reports whether path is a directory, following symlinks.
func IsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// IsExecutable reports whether path is a regular file with an execute bit,
// following symlinks.
func IsExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
