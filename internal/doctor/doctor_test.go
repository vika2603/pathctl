package doctor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func exe(t *testing.T, path string) string {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

type fixture struct {
	root, mine, sys, early, over string
}

func newFixture(t *testing.T) fixture {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	f := fixture{
		root:  root,
		mine:  filepath.Join(root, "mine"),  // a configured directory
		sys:   filepath.Join(root, "sys"),   // something else on PATH
		early: filepath.Join(root, "early"), // comes before the override dir
		over:  filepath.Join(root, "over"),
	}
	for _, d := range []string{f.mine, f.sys, f.early, f.over} {
		os.MkdirAll(d, 0o755)
	}
	return f
}

func TestConflicts(t *testing.T) {
	f := newFixture(t)
	exe(t, filepath.Join(f.mine, "only-mine"))
	exe(t, filepath.Join(f.mine, "tool"))
	sysTool := exe(t, filepath.Join(f.sys, "tool"))
	// The same file reached twice is not a conflict.
	exe(t, filepath.Join(f.mine, "shared"))
	os.Symlink(filepath.Join(f.mine, "shared"), filepath.Join(f.sys, "shared"))
	// Not executable, so not a command.
	os.WriteFile(filepath.Join(f.mine, "README"), nil, 0o644)
	os.WriteFile(filepath.Join(f.sys, "README"), nil, 0o644)

	got := Check(Input{Path: []string{f.over, f.sys, f.mine}, OverrideDir: f.over, Dirs: []string{f.mine}})
	want := []Problem{{Kind: Conflict, Name: "tool", Winner: sysTool, Others: []string{filepath.Join(f.mine, "tool")}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Check:\n got %+v\nwant %+v", got, want)
	}
}

func TestOverrides(t *testing.T) {
	f := newFixture(t)
	// pinned: link in place and effective.
	pinned := exe(t, filepath.Join(f.mine, "pinned"))
	exe(t, filepath.Join(f.sys, "pinned"))
	os.Symlink(pinned, filepath.Join(f.over, "pinned"))
	// lost: link in place, but early/lost comes first.
	lost := exe(t, filepath.Join(f.mine, "lost"))
	os.Symlink(lost, filepath.Join(f.over, "lost"))
	earlyLost := exe(t, filepath.Join(f.early, "lost"))
	// first: the chosen file is itself in a directory before the override
	// directory, so it runs; that is not a problem.
	first := exe(t, filepath.Join(f.early, "first"))
	// stale: the link still points at the previous choice.
	old := exe(t, filepath.Join(f.sys, "stale"))
	stale := exe(t, filepath.Join(f.mine, "stale"))
	os.Symlink(old, filepath.Join(f.over, "stale"))
	missing := filepath.Join(f.root, "missing")

	in := Input{
		Path:        []string{f.early, f.over, f.sys, f.mine},
		OverrideDir: f.over,
		Overrides: []Override{
			{Name: "pinned", Target: pinned},
			{Name: "lost", Target: lost},
			{Name: "first", Target: first},
			{Name: "stale", Target: stale},
			{Name: "gone", Target: missing},
			{Name: "unresolved", Target: ""},
		},
	}
	want := []Problem{
		{Kind: Shadowed, Name: "lost", Target: lost, Winner: earlyLost},
		{Kind: Stale, Name: "stale", Target: stale},
		{Kind: Broken, Name: "gone", Target: missing},
		{Kind: Broken, Name: "unresolved"},
	}
	if got := Check(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("Check:\n got %+v\nwant %+v", got, want)
	}

	// Without the override directory on PATH, only that is reported, plus
	// overrides that are broken regardless.
	in.Path = []string{f.early, f.sys, f.mine}
	want = []Problem{
		{Kind: NotOnPath, Target: f.over},
		{Kind: Broken, Name: "gone", Target: missing},
		{Kind: Broken, Name: "unresolved"},
	}
	if got := Check(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("Check without the override dir:\n got %+v\nwant %+v", got, want)
	}
}

func TestConfiguredDirNotOnPath(t *testing.T) {
	f := newFixture(t)
	exe(t, filepath.Join(f.mine, "py"))
	exe(t, filepath.Join(f.sys, "py"))
	exe(t, filepath.Join(f.early, "py"))

	got := Check(Input{Path: []string{f.over, f.early, f.sys}, OverrideDir: f.over, Dirs: []string{f.mine}})
	want := []Problem{{Kind: DirNotOnPath, Target: f.mine}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Check:\n got %+v\nwant %+v", got, want)
	}
}
