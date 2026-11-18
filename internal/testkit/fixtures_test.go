package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtures_FixtureFileOutsideIsRefused(t *testing.T) {
	e, outside := sandboxed(t)
	refused(t, "WriteStore", func() { e.WriteStore(outside+"/.shortcuts", map[string]any{}) })
	refused(t, "Symlink", func() { e.Symlink(e.Tmp, outside+"/l") })
	if n := len(entries(t, outside)); n != 0 {
		t.Errorf("outside has %d entries", n)
	}
}

func TestFixtures_RegistryHelpers(t *testing.T) {
	e := New(t)
	proj := e.Project(GO)
	store := filepath.Join(proj, ".shortcuts")
	// A project lists an import in .shortcuts; the global store just finds it.
	p := e.WriteImport(store, "docker", DockerPS)
	raw, _ := os.ReadFile(store)
	if p != filepath.Join(proj, "imports", "docker.shortcut") || !strings.Contains(string(raw), `"imports":{"docker":"imports/docker.shortcut"}`) {
		t.Errorf("project import: %s, store %s", p, raw)
	}
	if g := e.WriteImport(e.Store, "kube", KubeGet); g != filepath.Join(e.Store, "imports", "kube.shortcut") {
		t.Errorf("global import: %s", g)
	}
	// Keys come out sorted and <, > and & stay as written.
	e.writeRegistry(e.Store, map[string]any{"z": "<&>", "a": 1})
	if b, _ := os.ReadFile(filepath.Join(e.Store, "shortcuts.json")); string(b) != `{"a":1,"z":"<&>"}` {
		t.Errorf("registry = %s", b)
	}
}
