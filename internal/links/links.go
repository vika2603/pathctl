// Package links keeps the override directory in line with the configuration.
package links

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Link asks for dir/Name to point at Target.
type Link struct {
	Name   string
	Target string
}

// Sync makes dir hold exactly one symlink per link and removes every other
// symlink, including temporary ones left by an interrupted run. Links whose
// target is not an existing file are left out, so the command falls back to
// its normal PATH lookup. Regular files in dir are never touched.
//
// Callers must hold the lock from package lock: removing unknown symlinks
// would otherwise race with another process placing its own.
func Sync(dir string, want []Link) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	keep := map[string]bool{}
	var errs []error
	for _, l := range want {
		if info, err := os.Stat(l.Target); err != nil || info.IsDir() {
			continue
		}
		keep[l.Name] = true
		if err := place(dir, l); err != nil {
			errs = append(errs, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, e := range entries {
		if e.Type()&fs.ModeSymlink == 0 || keep[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Occupied reports an error when dir/name exists and is not a symlink, which
// Sync would refuse to replace.
func Occupied(dir, name string) error {
	info, err := os.Lstat(filepath.Join(dir, name))
	if err == nil && info.Mode()&fs.ModeSymlink == 0 {
		return fmt.Errorf("%s exists and is not a link made by pathctl; not replacing it", filepath.Join(dir, name))
	}
	return nil
}

// place points dir/l.Name at l.Target, replacing an existing symlink through
// a rename so the name never disappears.
func place(dir string, l Link) error {
	path := filepath.Join(dir, l.Name)
	if current, err := os.Readlink(path); err == nil && current == l.Target {
		return nil
	}
	if err := Occupied(dir, l.Name); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+l.Name+".pathctl-"+strconv.Itoa(os.Getpid())+"-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	if err := os.Symlink(l.Target, tmp); err != nil {
		return fmt.Errorf("link %s: %w", l.Name, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("link %s: %w", l.Name, err)
	}
	return nil
}
