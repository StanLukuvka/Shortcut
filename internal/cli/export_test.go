package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestExport_UpdateWarnsAboutAnExportedRelativeCwdAndApproves(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"rel": map[string]any{"inline": "pwd", "cwd": "sub", "export": true}}, testkit.Dirs("sub"))
	res := runOK(t, e, in(proj), "update", "-y")
	testkit.Contains(t, "stderr", res.Stderr, "warning: 'rel' is exported, but its cwd 'sub' only exists in this checkout")
	testkit.Contains(t, "stdout", res.Stdout, "Approved")
	testkit.Eq(t, "run", e.RunWith(in(proj), "r", "rel").Stdout, filepath.Join(proj, "sub")+"\n")
}

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
	testkit.Eq(t, "run", e.RunWith(in(proj), "r", "abs").Stdout, "abs\n")
	testkit.Contains(t, "show", e.RunWith(in(proj), "show", "abs").Stdout, "Export:      yes")
}

func TestExport_TheLocalFileCanExport(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(nil)
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), docText(t, testkit.NS(map[string]any{
		"mine": map[string]any{"inline": "echo mine", "export": true}})), 0)
	e.Approve(filepath.Join(proj, ".shortcuts"))
	testkit.Eq(t, "run", e.RunWith(in(proj), "r", "mine").Stdout, "mine\n")
}

func TestExport_IsRefusedInTheGlobalStoreAndNamespaces(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"g": map[string]any{"inline": "echo g", "export": true}})
	res := e.Run("r", "g")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "'export' is only for .shortcuts and .shortcuts.local")
	testkit.Contains(t, "list", e.Run("list").Stderr, "'export' is only for")

	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{
		"ps": map[string]any{"inline": "echo ps", "export": true}}))
	res = e.Run("update", "docker", "-y")
	testkit.Eq(t, "ns exit", res.Code, 1)
	testkit.Contains(t, "ns", res.Stderr, "'export' is only for")
}

func TestExport_MustBeABoolean(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"x": map[string]any{"inline": "echo x", "export": "yes"}})
	res := e.RunWith(in(proj), "update", ".", "-y")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "'export' must be a boolean")
}

// treeOf is every file under root: its path, mode and bytes.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		out[rel] = info.Mode().String() + "\n" + string(data)
		return nil
	})
	return out
}
