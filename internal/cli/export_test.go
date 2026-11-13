package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// py: test_export.py::test_update_warns_about_an_exported_relative_cwd_and_approves
func TestExport_UpdateWarnsAboutAnExportedRelativeCwdAndApproves(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"rel": map[string]any{"inline": "pwd", "cwd": "sub", "export": true}}, testkit.Dirs("sub"))
	res := runOK(t, e, in(proj), "update", "-y")
	contains(t, "stderr", res.Stderr, "warning: 'rel' is exported, but its cwd 'sub' only exists in this checkout")
	contains(t, "stdout", res.Stdout, "Approved")
	eq(t, "run", e.RunWith(in(proj), "r", "rel").Stdout, filepath.Join(proj, "sub")+"\n")
}

// py: test_export.py::test_update_doesnt_warn_about_other_cwds
func TestExport_UpdateDoesntWarnAboutOtherCwds(t *testing.T) {
	e := testkit.New(t)
	fixed := e.Folder("fixed")
	proj := e.Project(map[string]any{
		"abs":    map[string]any{"inline": "echo abs", "cwd": fixed, "export": true},
		"here":   map[string]any{"inline": "echo here", "cwd": "caller", "export": true},
		"copied": map[string]any{"script": "bin/tool", "export": true},
		"mine":   map[string]any{"inline": "echo mine", "cwd": "sub"},
	}, testkit.Dirs("sub"), testkit.Files(map[string]any{"bin/tool": "#!/bin/sh\necho tool\n"}))
	res := runOK(t, e, in(proj), "update", "-y")
	if strings.Contains(res.Stderr, "warning") {
		t.Errorf("warning: %q", res.Stderr)
	}
	eq(t, "run", e.RunWith(in(proj), "r", "abs").Stdout, "abs\n")
	contains(t, "show", e.RunWith(in(proj), "show", "abs").Stdout, "Export:      True")
}

// py: test_export.py::test_the_local_file_can_export
func TestExport_TheLocalFileCanExport(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(nil)
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), docText(t, map[string]any{"version": 2, "shortcuts": map[string]any{
		"mine": map[string]any{"inline": "echo mine", "export": true}}}), 0)
	e.Approve(filepath.Join(proj, ".shortcuts"))
	eq(t, "run", e.RunWith(in(proj), "r", "mine").Stdout, "mine\n")
}

// py: test_export.py::test_export_is_refused_in_the_global_store_and_namespaces
func TestExport_IsRefusedInTheGlobalStoreAndNamespaces(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"g": map[string]any{"inline": "echo g", "export": true}})
	res := e.Run("r", "g")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "'export' is only for .shortcuts and .shortcuts.local")
	contains(t, "list", e.Run("list").Stderr, "'export' is only for")

	e.WriteImport(e.Store, "docker", map[string]any{"version": 2, "shortcuts": map[string]any{
		"ps": map[string]any{"inline": "echo ps", "export": true}}})
	res = e.Run("update", "docker", "-y")
	eq(t, "ns exit", res.Code, 1)
	contains(t, "ns", res.Stderr, "'export' is only for")
}

// py: test_export.py::test_export_must_be_a_boolean
func TestExport_MustBeABoolean(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"x": map[string]any{"inline": "echo x", "export": "yes"}})
	res := e.RunWith(in(proj), "update", "-y")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "'export' must be a boolean")
}
