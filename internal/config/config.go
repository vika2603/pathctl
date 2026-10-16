// Package config reads and edits the pathctl configuration file.
//
// The file holds one directive per line:
//
//	dir <path>          a directory to append to PATH
//	use <name> <path>   the file to run for command <name>
//
// Lines starting with # are comments. Edits keep comments and blank lines.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// Kind tells what a line declares.
type Kind int

const (
	Other Kind = iota // comment or blank line
	Dir
	Use
)

// Line is one line of the file. Path is the path as written; Name is the
// command name of a Use line.
type Line struct {
	Raw  string
	Kind Kind
	Name string
	Path string
}

// Override pins a command name to a file.
type Override struct {
	Name string
	Path string
}

// File is a parsed configuration file.
type File struct {
	Path  string
	Lines []Line
}

// Load reads and parses the file at path. A missing file is an empty
// configuration.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &File{Path: path}, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(path, data)
}

// Parse parses data read from path. Errors name the file and line.
func Parse(path string, data []byte) (*File, error) {
	f := &File{Path: path}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return f, nil
	}
	used := map[string]int{}
	for i, raw := range strings.Split(text, "\n") {
		line, err := parseLine(strings.TrimSuffix(raw, "\r"))
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, i+1, err)
		}
		if line.Kind == Use {
			if prev, ok := used[line.Name]; ok {
				return nil, fmt.Errorf("%s:%d: %q is already set on line %d", path, i+1, line.Name, prev)
			}
			used[line.Name] = i + 1
		}
		f.Lines = append(f.Lines, line)
	}
	return f, nil
}

func parseLine(raw string) (Line, error) {
	line := Line{Raw: raw}
	text := strings.TrimSpace(raw)
	if text == "" || strings.HasPrefix(text, "#") {
		return line, nil
	}
	word, rest := cutField(text)
	switch word {
	case "dir":
		if rest == "" {
			return line, errors.New("dir needs a directory")
		}
		if err := CheckPath(rest); err != nil {
			return line, err
		}
		line.Kind, line.Path = Dir, rest
	case "use":
		name, path := cutField(rest)
		if name == "" || path == "" {
			return line, errors.New("use needs a command name and a file")
		}
		if err := ValidName(name); err != nil {
			return line, err
		}
		if err := CheckPath(path); err != nil {
			return line, err
		}
		line.Kind, line.Name, line.Path = Use, name, path
	default:
		return line, fmt.Errorf("unknown directive %q", word)
	}
	return line, nil
}

// cutField splits s at its first run of white space.
func cutField(s string) (field, rest string) {
	i := strings.IndexFunc(s, unicode.IsSpace)
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i:])
}

// ValidName reports whether name can be a command name: no slash, white
// space or control character, and not . or ..
func ValidName(name string) error {
	bad := strings.IndexFunc(name, func(r rune) bool {
		return r == '/' || unicode.IsSpace(r) || unicode.IsControl(r)
	})
	if name == "" || name == "." || name == ".." || bad >= 0 {
		return fmt.Errorf("invalid command name %q", name)
	}
	return nil
}

// Dirs returns the directories in file order.
func (f *File) Dirs() []string {
	var dirs []string
	for _, l := range f.Lines {
		if l.Kind == Dir {
			dirs = append(dirs, l.Path)
		}
	}
	return dirs
}

// Overrides returns the overrides in file order.
func (f *File) Overrides() []Override {
	var out []Override
	for _, l := range f.Lines {
		if l.Kind == Use {
			out = append(out, Override{Name: l.Name, Path: l.Path})
		}
	}
	return out
}

// HasDir reports whether a configured directory refers to path.
func (f *File) HasDir(path string, expand func(string) string) bool {
	for _, l := range f.Lines {
		if l.Kind == Dir && sameDir(l.Path, path, expand) {
			return true
		}
	}
	return false
}

// sameDir matches paths written the same way, so an entry whose variable is
// unset can still be named, or that expand to the same directory.
func sameDir(a, b string, expand func(string) string) bool {
	if a == b {
		return true
	}
	ea := expand(a)
	return ea != "" && ea == expand(b)
}

// AddDir appends a dir line after the last one, or at the end of the file.
func (f *File) AddDir(path string) {
	f.insertAfterLast(Dir, Line{Raw: "dir " + path, Kind: Dir, Path: path})
}

// RemoveDir deletes every line that refers to path and reports whether any
// was found.
func (f *File) RemoveDir(path string, expand func(string) string) bool {
	kept := f.Lines[:0]
	for _, l := range f.Lines {
		if l.Kind != Dir || !sameDir(l.Path, path, expand) {
			kept = append(kept, l)
		}
	}
	removed := len(kept) < len(f.Lines)
	f.Lines = kept
	return removed
}

// SetOverride replaces the use line for name, or adds one after the last use
// line.
func (f *File) SetOverride(name, path string) {
	line := Line{Raw: "use " + name + " " + path, Kind: Use, Name: name, Path: path}
	for i, l := range f.Lines {
		if l.Kind == Use && l.Name == name {
			f.Lines[i] = line
			return
		}
	}
	f.insertAfterLast(Use, line)
}

// RemoveOverride deletes the use line for name and reports whether one was
// found.
func (f *File) RemoveOverride(name string) bool {
	for i, l := range f.Lines {
		if l.Kind == Use && l.Name == name {
			f.Lines = append(f.Lines[:i], f.Lines[i+1:]...)
			return true
		}
	}
	return false
}

func (f *File) insertAfterLast(kind Kind, line Line) {
	at := len(f.Lines)
	for i := len(f.Lines) - 1; i >= 0; i-- {
		if f.Lines[i].Kind == kind {
			at = i + 1
			break
		}
	}
	f.Lines = append(f.Lines[:at], append([]Line{line}, f.Lines[at:]...)...)
}

// Bytes renders the file.
func (f *File) Bytes() []byte {
	var b bytes.Buffer
	for _, l := range f.Lines {
		b.WriteString(l.Raw)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// Save writes the file atomically. When the file is a symlink, the file it
// points to is written, even if it does not exist yet, and the link is kept.
func (f *File) Save() (err error) {
	target, err := linkTarget(f.Path)
	if err != nil {
		return err
	}
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tmp.Name())
		}
	}()
	if _, err = tmp.Write(f.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

// linkTarget follows symlinks at path, including ones whose target does not
// exist yet, and returns the path to write.
func linkTarget(path string) (string, error) {
	for range 40 {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&fs.ModeSymlink == 0 {
			return path, nil
		}
		dest, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(dest) {
			dest = filepath.Join(filepath.Dir(path), dest)
		}
		path = dest
	}
	return "", fmt.Errorf("%s: too many levels of symbolic links", path)
}
