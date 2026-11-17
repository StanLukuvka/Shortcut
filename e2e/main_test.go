package e2e

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func realpath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func docText(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// fileShortcuts is the "shortcuts" object of a store or namespace file, as the test sees it.
func fileShortcuts(t *testing.T, path string) map[string]any {
	t.Helper()
	doc := decodeObject(t, testkit.ReadFile(t, path))
	s, _ := doc["shortcuts"].(map[string]any)
	return s
}

// sharedEntries is the shared set of entries these tests use.
func sharedEntries() map[string]any {
	return map[string]any{
		"keep": map[string]any{"description": "shared keep", "inline": "echo shared keep"},
		"swap": map[string]any{"description": "shared swap", "inline": "echo shared swap", "confirm": true},
	}
}

func writeLocalFile(t *testing.T, e *testkit.Env, proj string, shortcuts map[string]any) {
	t.Helper()
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), docText(t, testkit.NS(shortcuts)), 0)
}

func localData(t *testing.T, proj string) map[string]any {
	t.Helper()
	return fileShortcuts(t, filepath.Join(proj, ".shortcuts.local"))
}

// overlaid is a project with a .shortcuts and a .shortcuts.local written by hand.
func overlaid(t *testing.T, e *testkit.Env) string {
	t.Helper()
	proj := e.Project(sharedEntries(), testkit.Dirs("src"))
	writeLocalFile(t, e, proj, map[string]any{"mine": testkit.Inline("echo mine"), "swap": testkit.Inline("echo local swap")})
	return proj
}

const bashShebang = "#!/usr/bin/env bash\n"

func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }

func y(text string) []byte { return []byte(text) }

// runOK runs the binary and fails the test unless it exits 0: for set-up that goes through a command.
func runOK(t *testing.T, e *testkit.Env, o testkit.Opts, args ...string) testkit.Result {
	t.Helper()
	res := e.RunWith(o, args...)
	if res.Code != 0 {
		t.Fatalf("%v: exit %d\nstdout: %s\nstderr: %s", args, res.Code, res.Stdout, res.Stderr)
	}
	return res
}

func in(dir string) testkit.Opts { return testkit.Opts{Cwd: dir} }

func decodeObject(t *testing.T, text string) map[string]any {
	t.Helper()
	var info map[string]any
	if err := json.Unmarshal([]byte(text), &info); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, text)
	}
	return info
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
