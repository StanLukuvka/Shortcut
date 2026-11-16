package commands

import (
	"io/fs"
	"path/filepath"
	"slices"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func tree(t *testing.T, roots ...string) []string {
	t.Helper()
	var paths []string
	for _, root := range roots {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			paths = append(paths, p)
			return nil
		})
	}
	slices.Sort(paths)
	return paths
}

func TestSymlinkSafety_ARunWritesNothingInTheProjectOrTheStore(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(testkit.GO)
	before1, before2 := tree(t, e.Store), tree(t, proj)
	if got := e.RunWith(in(proj), "r", ".", "go").Stdout; got != "ran\n" {
		t.Errorf("run: %q", got)
	}
	if after := tree(t, e.Store); !slices.Equal(before1, after) {
		t.Errorf("the store changed: %v -> %v", before1, after)
	}
	if after := tree(t, proj); !slices.Equal(before2, after) {
		t.Errorf("the project changed: %v -> %v", before2, after)
	}
}

func TestSymlinkSafety_LocalNeverWritesThroughACommittedSymlink(t *testing.T) {
	e := testkit.New(t)
	victim := e.WriteFile(filepath.Join(e.Tmp, "victim"), "keep\n", 0)
	proj := e.Project(map[string]any{})
	e.Symlink(victim, filepath.Join(proj, ".shortcuts.local"))
	res := e.RunWith(in(proj), "new", "build", "--local", "--", "true")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "is a symlink")
	testkit.Eq(t, "victim", testkit.ReadFile(t, victim), "keep\n")
}
