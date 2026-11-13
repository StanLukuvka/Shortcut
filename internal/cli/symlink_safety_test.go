// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-3). Don't hand-edit without checking the plan.

package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// py: test_symlink_safety.py::test_a_run_writes_nothing_in_the_project_or_the_store
func TestSymlinkSafety_ARunWritesNothingInTheProjectOrTheStore(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(testkit.GO)
	e.Approve(filepath.Join(proj, ".shortcuts"))
	before1, before2 := tree(t, e.Store), tree(t, proj)
	if got := e.RunWith(in(proj), "r", "go").Stdout; got != "ran\n" {
		t.Errorf("run: %q", got)
	}
	if after := tree(t, e.Store); !slices.Equal(before1, after) {
		t.Errorf("the store changed: %v -> %v", before1, after)
	}
	if after := tree(t, proj); !slices.Equal(before2, after) {
		t.Errorf("the project changed: %v -> %v", before2, after)
	}
}

// py: test_symlink_safety.py::test_moving_a_symlink_under_a_relative_cwd_needs_approval
func TestSymlinkSafety_MovingASymlinkUnderARelativeCwdNeedsApproval(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"go": map[string]any{"inline": "pwd", "cwd": "work"}}, testkit.Dirs("safe"))
	elsewhere := e.Folder("elsewhere")
	e.Symlink(filepath.Join(proj, "safe"), filepath.Join(proj, "work"))
	e.Approve(filepath.Join(proj, ".shortcuts"))
	res := e.RunWith(in(proj), "r", "go")
	if res.Code != 0 {
		t.Fatalf("exit %d %s", res.Code, res.Stderr)
	}
	contains(t, "stderr", res.Stderr, "→ "+filepath.Join(proj, "safe"))

	os.Remove(filepath.Join(proj, "work"))
	e.Symlink(elsewhere, filepath.Join(proj, "work"))
	if code := e.RunWith(in(proj), "r", "go").Code; code != 1 {
		t.Errorf("moved symlink: exit %d", code)
	}
	res = e.RunWith(in(proj), "update", "-y")
	contains(t, "diff", res.Stdout, "+"+elsewhere)
	contains(t, "diff", res.Stdout, "--- a/cwd of go\n")
	if code := e.RunWith(in(proj), "r", "go").Code; code != 0 {
		t.Errorf("after update: exit %d", code)
	}
}
