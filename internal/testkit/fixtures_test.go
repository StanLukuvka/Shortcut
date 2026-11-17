package testkit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// decoded is v as a JSON round trip makes it, to compare with what a file holds.
func decoded(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFixtures_FixtureFileOutsideIsRefused(t *testing.T) {
	e, outside := sandboxed(t)
	refused(t, "WriteStore", func() { e.WriteStore(outside+"/.shortcuts", map[string]any{}) })
	refused(t, "Symlink", func() { e.Symlink(e.Tmp, outside+"/l") })
	if n := len(entries(t, outside)); n != 0 {
		t.Errorf("outside has %d entries", n)
	}
}

func TestFixtures_ProjectWritesWhatItIsGiven(t *testing.T) {
	e := New(t)
	imports := map[string]any{"a": "a.shortcut"}
	proj := e.Project(GO, Dirs("src"), Files(map[string]any{"bin/tool": []byte{0xff}}), Extra(map[string]any{"imports": imports}))
	if fi, err := os.Stat(filepath.Join(proj, "src")); err != nil || !fi.IsDir() {
		t.Errorf("src: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(proj, "bin", "tool")); string(b) != "\xff" {
		t.Errorf("bin/tool = %q", b)
	}
	raw, err := os.ReadFile(filepath.Join(proj, ".shortcuts"))
	if err != nil {
		t.Fatal(err)
	}
	var got any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := decoded(t, map[string]any{"version": 1, "shortcuts": GO, "imports": imports})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("store = %v, want %v", got, want)
	}
}

func TestFixtures_ProjectWithoutShortcutsHasNoStore(t *testing.T) {
	e := New(t)
	if _, err := os.Stat(filepath.Join(e.Project(nil), ".shortcuts")); err == nil {
		t.Error("a store was written")
	}
}

func TestFixtures_BrokenProjectReadsAsCorrupt(t *testing.T) {
	e := New(t) // the "corrupt registry" message itself
	if b, _ := os.ReadFile(filepath.Join(e.BrokenProject(), ".shortcuts")); string(b) != brokenJSON {
		t.Errorf("store = %q", b)
	}
}

func TestFixtures_SharedFixturesAreLeftAsTheyWere(t *testing.T) {
	e := New(t)
	useBinary(t, e, "exit 0\n")
	before := fmt.Sprint(GO, HI)
	e.Project(GO)
	if after := fmt.Sprint(GO, HI); after != before {
		t.Errorf("fixtures changed: %s -> %s", before, after)
	}
}

func TestFixtures_RegistryHelpers(t *testing.T) {
	e := New(t)
	proj := e.Project(GO)
	store := filepath.Join(proj, ".shortcuts")
	if e.registryPath(store) != store || e.registryPath(e.Store) != filepath.Join(e.Store, "shortcuts.json") ||
		e.registryPath(proj) != filepath.Join(proj, "shortcuts.json") {
		t.Error("RegistryPath")
	}
	e.WriteStore(e.Store, OWN)
	if got := e.LoadGlobal(); !reflect.DeepEqual(got, decoded(t, OWN)) {
		t.Errorf("LoadGlobal = %v", got)
	}
	// A project lists an import in .shortcuts; the global store just finds it.
	p := e.WriteImport(store, "docker", DockerPS)
	raw, _ := os.ReadFile(store)
	if p != filepath.Join(proj, "imports", "docker.shortcut") || !strings.Contains(string(raw), `"imports":{"docker":"imports/docker.shortcut"}`) {
		t.Errorf("project import: %s, store %s", p, raw)
	}
	if g := e.WriteImport(e.Store, "kube", KubeGet); g != filepath.Join(e.Store, "imports", "kube.shortcut") {
		t.Errorf("global import: %s", g)
	}
	// <, > and & stay as written, and a raw file keeps its key order.
	e.writeRegistry(e.Store, map[string]any{"a": "<&>"})
	e.WriteRegistryRaw(e.Store, `{"z":1,"a":2}`)
	if b, _ := os.ReadFile(filepath.Join(e.Store, "shortcuts.json")); string(b) != `{"z":1,"a":2}` {
		t.Errorf("raw = %s", b)
	}
}
