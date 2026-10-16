// Package pathenv computes the PATH a shell should use.
package pathenv

import "slices"

// Compute returns the new PATH entries and the directories pathctl is now
// responsible for.
//
// The override directory goes first. Every other existing entry keeps its
// relative order; only duplicates and directories that pathctl appended
// earlier (managed) but that are no longer configured are dropped. Configured
// directories that are not on PATH yet are appended. A directory that was
// already on PATH for another reason is left where it is and not recorded, so
// removing it from the configuration later does not take it off PATH.
//
// dirs must already be expanded and limited to existing directories.
// Computing again from the result changes nothing.
func Compute(current []string, overrideDir string, managed, dirs []string) (path, nowManaged []string) {
	seen := map[string]bool{overrideDir: true}
	path = []string{overrideDir}
	for _, p := range current {
		if seen[p] || (slices.Contains(managed, p) && !slices.Contains(dirs, p)) {
			continue
		}
		seen[p] = true
		path = append(path, p)
	}
	for _, d := range dirs {
		switch {
		case !seen[d]:
			seen[d] = true
			path = append(path, d)
			nowManaged = append(nowManaged, d)
		case slices.Contains(managed, d) && !slices.Contains(nowManaged, d):
			nowManaged = append(nowManaged, d)
		}
	}
	return path, nowManaged
}
