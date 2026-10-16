// Package doctor finds commands whose resolution on PATH is not decided by
// the configuration.
package doctor

import (
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/vika2603/pathctl/internal/config"
)

// Kind classifies a problem.
type Kind int

const (
	// NotOnPath: overrides exist but the override directory is not on PATH.
	NotOnPath Kind = iota
	// DirNotOnPath: a configured directory exists but is not on PATH.
	DirNotOnPath
	// Broken: an override points at a file that is missing or not executable.
	Broken
	// Blocked: a regular file in the override directory has the override's
	// name, so the link cannot be placed.
	Blocked
	// Stale: the link for an override is missing or points elsewhere.
	Stale
	// Shadowed: the link is right, but a directory before the override
	// directory provides a different file with the same name (Winner).
	Shadowed
	// Conflict: a command in a configured directory also exists elsewhere on
	// PATH as a different file, and no override decides which one runs.
	Conflict
)

// Problem is one finding. Target is the chosen file or the directory
// concerned; Winner is the file that runs; Others are the other files with the
// same name, in PATH order.
type Problem struct {
	Kind   Kind
	Name   string
	Target string
	Winner string
	Others []string
}

// Override is a configured override with its target expanded; Target is ""
// when it cannot be resolved.
type Override struct {
	Name   string
	Target string
}

// Input describes the current state. Path is the PATH being checked; Dirs are
// the configured directories that exist, expanded.
type Input struct {
	Path        []string
	OverrideDir string
	Overrides   []Override
	Dirs        []string
}

// Check returns the problems in a stable order.
func Check(in Input) []Problem {
	var problems []Problem
	idx := newIndex(in.Path)
	onPath := slices.Contains(in.Path, in.OverrideDir)

	if len(in.Overrides) > 0 && !onPath {
		problems = append(problems, Problem{Kind: NotOnPath, Target: in.OverrideDir})
	}
	for _, d := range in.Dirs {
		if !slices.Contains(in.Path, d) {
			problems = append(problems, Problem{Kind: DirNotOnPath, Target: d})
		}
	}

	overridden := map[string]bool{}
	for _, o := range in.Overrides {
		overridden[o.Name] = true
		if !config.IsExecutable(o.Target) {
			problems = append(problems, Problem{Kind: Broken, Name: o.Name, Target: o.Target})
			continue
		}
		if !onPath {
			continue
		}
		found := idx.lookup(o.Name)
		if len(found) > 0 && sameFile(found[0], o.Target) {
			continue
		}
		link := filepath.Join(in.OverrideDir, o.Name)
		if info, err := os.Lstat(link); err == nil && info.Mode()&os.ModeSymlink == 0 {
			problems = append(problems, Problem{Kind: Blocked, Name: o.Name, Target: link})
			continue
		}
		if !sameFile(link, o.Target) {
			problems = append(problems, Problem{Kind: Stale, Name: o.Name, Target: o.Target})
			continue
		}
		problems = append(problems, Problem{Kind: Shadowed, Name: o.Name, Target: o.Target, Winner: found[0]})
	}

	reported := map[string]bool{}
	for _, dir := range in.Dirs {
		if !slices.Contains(in.Path, dir) {
			continue
		}
		for _, name := range idx.names(dir) {
			if overridden[name] || reported[name] {
				continue
			}
			reported[name] = true
			var files []string
			for _, f := range idx.lookup(name) {
				if filepath.Dir(f) == in.OverrideDir || slices.ContainsFunc(files, func(g string) bool { return sameFile(f, g) }) {
					continue
				}
				files = append(files, f)
			}
			if len(files) > 1 {
				problems = append(problems, Problem{Kind: Conflict, Name: name, Winner: files[0], Others: files[1:]})
			}
		}
	}
	return problems
}

// index lists each PATH directory once.
type index struct {
	dirs    []string
	entries map[string]map[string]bool
}

func newIndex(path []string) *index {
	idx := &index{entries: map[string]map[string]bool{}}
	for _, dir := range path {
		if dir == "" || idx.entries[dir] != nil {
			continue
		}
		set := map[string]bool{}
		if list, err := os.ReadDir(dir); err == nil {
			for _, e := range list {
				set[e.Name()] = true
			}
		}
		idx.entries[dir] = set
		idx.dirs = append(idx.dirs, dir)
	}
	return idx
}

// lookup returns every executable file called name, in PATH order.
func (idx *index) lookup(name string) []string {
	var found []string
	for _, dir := range idx.dirs {
		if idx.entries[dir][name] {
			if f := filepath.Join(dir, name); config.IsExecutable(f) {
				found = append(found, f)
			}
		}
	}
	return found
}

// names returns the executable names in a directory on PATH, sorted.
func (idx *index) names(dir string) []string {
	var names []string
	for n := range idx.entries[dir] {
		if config.IsExecutable(filepath.Join(dir, n)) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

func sameFile(a, b string) bool {
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	return err == nil && os.SameFile(ia, ib)
}
