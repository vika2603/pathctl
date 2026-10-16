package links

import (
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, path string) string {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readlink(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return target
}

func TestSync(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bin")
	a1 := touch(t, filepath.Join(root, "a1", "tool"))
	a2 := touch(t, filepath.Join(root, "a2", "tool"))
	other := touch(t, filepath.Join(root, "other"))

	if err := Sync(dir, []Link{{"tool", a1}, {"other", other}}); err != nil {
		t.Fatal(err)
	}
	if readlink(t, filepath.Join(dir, "tool")) != a1 || readlink(t, filepath.Join(dir, "other")) != other {
		t.Fatal("links not created")
	}

	// A regular file in the directory is not pathctl's to remove.
	touch(t, filepath.Join(dir, "keep"))
	if err := Sync(dir, []Link{{"tool", a2}, {"gone", filepath.Join(root, "missing")}}); err != nil {
		t.Fatal(err)
	}
	if got := readlink(t, filepath.Join(dir, "tool")); got != a2 {
		t.Errorf("tool -> %q, want %q", got, a2)
	}
	if _, err := os.Lstat(filepath.Join(dir, "other")); !os.IsNotExist(err) {
		t.Error("stale link other was not removed")
	}
	if _, err := os.Lstat(filepath.Join(dir, "gone")); !os.IsNotExist(err) {
		t.Error("link to a missing target was created")
	}
	if _, err := os.Stat(filepath.Join(dir, "keep")); err != nil {
		t.Error("regular file was removed")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("directory holds %d entries, want tool and keep", len(entries))
	}
}

func TestSyncNeverReplacesRegularFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bin")
	mine := touch(t, filepath.Join(dir, "mine"))
	target := touch(t, filepath.Join(root, "tool"))

	if err := Occupied(dir, "mine"); err == nil {
		t.Error("Occupied did not report the regular file")
	}
	if err := Sync(dir, []Link{{"mine", target}}); err == nil {
		t.Error("Sync replaced a regular file without an error")
	}
	if fi, err := os.Lstat(mine); err != nil || !fi.Mode().IsRegular() {
		t.Fatal("regular file was replaced")
	}
}

func TestSyncRemovesLeftoverTemporaryLinks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bin")
	os.MkdirAll(dir, 0o755)
	os.Symlink("/nowhere", filepath.Join(dir, ".tool.pathctl-1-abc"))
	if err := Sync(dir, nil); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left %v", entries)
	}
}
