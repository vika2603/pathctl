package pathenv

import (
	"reflect"
	"testing"
)

func TestCompute(t *testing.T) {
	cases := []struct {
		name                   string
		current, managed, dirs []string
		wantPath, wantManaged  []string
	}{
		{
			name:        "appends missing dirs after everything else",
			current:     []string{"/usr/bin", "/bin"},
			dirs:        []string{"/a", "/b"},
			wantPath:    []string{"/o", "/usr/bin", "/bin", "/a", "/b"},
			wantManaged: []string{"/a", "/b"},
		},
		{
			name:        "never moves an entry that is already there",
			current:     []string{"/b", "/usr/bin", "/o", "/usr/bin"},
			dirs:        []string{"/a", "/b"},
			wantPath:    []string{"/o", "/b", "/usr/bin", "/a"},
			wantManaged: []string{"/a"},
		},
		{
			name:        "drops dirs it appended that are no longer configured",
			current:     []string{"/o", "/usr/bin", "/a", "/b"},
			managed:     []string{"/a", "/b"},
			dirs:        []string{"/b"},
			wantPath:    []string{"/o", "/usr/bin", "/b"},
			wantManaged: []string{"/b"},
		},
		{
			name:        "keeps dirs that were on PATH for another reason",
			current:     []string{"/usr/bin", "/a"},
			dirs:        nil,
			wantPath:    []string{"/o", "/usr/bin", "/a"},
			wantManaged: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path, managed := Compute(c.current, "/o", c.managed, c.dirs)
			if !reflect.DeepEqual(path, c.wantPath) || !reflect.DeepEqual(managed, c.wantManaged) {
				t.Fatalf("Compute = %q, %q; want %q, %q", path, managed, c.wantPath, c.wantManaged)
			}
			again, againManaged := Compute(path, "/o", managed, c.dirs)
			if !reflect.DeepEqual(again, path) || !reflect.DeepEqual(againManaged, managed) {
				t.Fatalf("not idempotent: %q, %q", again, againManaged)
			}
		})
	}
}
