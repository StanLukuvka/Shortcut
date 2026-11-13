// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-2). Don't hand-edit without checking the plan.

package model

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/pyos"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func inline(text string) map[string]any { return map[string]any{"inline": text} }

func wantErr(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got error %v, want one containing %q", err, want)
	}
}

// py: test_stores.py::test_scope_resolution_order
func TestStores_ScopeResolutionOrder(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"build": map[string]any{"description": "Global build", "inline": "echo global_build"}})
	proj := filepath.Join(e.Home, "my_project")
	e.WriteStore(filepath.Join(proj, ".shortcuts"), map[string]any{"build": map[string]any{"description": "Local project build", "inline": "echo local_build"}})

	// Inside a project: project precedence
	res, _, err := ResolveShortcut([]string{"build"}, proj, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Group.StoreDir != pyos.Realpath(proj) || res.Entry.Description != "Local project build" || !res.HidesGlobal {
		t.Errorf("inside the project: %+v %q", res.Group.StoreDir, res.Entry.Description)
	}
	// --global reaches the global one from there (Python also ran `run`, which refuses the unapproved project: P-3)
	res, _, err = ResolveShortcut([]string{"build"}, proj, true, false)
	if err != nil || res.Entry.Description != "Global build" || res.HidesGlobal {
		t.Errorf("--global: %v", err)
	}

	// Outside the project: the global one
	res, _, err = ResolveShortcut([]string{"build"}, e.Home, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Group.StoreDir != pyos.Realpath(e.Store) || res.Entry.Description != "Global build" {
		t.Errorf("outside: %q %q", res.Group.StoreDir, res.Entry.Description)
	}
	_, _, err = ResolveShortcut([]string{"check"}, e.Home, false, false)
	wantErr(t, err, "shortcut 'check' not found")
}

// py: test_stores.py::test_a_name_in_an_outer_project_is_pointed_at
func TestHidingAndNotFound_ANameInAnOuterProjectIsPointedAt(t *testing.T) {
	e := testkit.New(t)
	outer := e.Project(map[string]any{"outer": inline("echo outer")}, testkit.Name("outer"))
	inner := filepath.Join(outer, "vendor")
	e.WriteStore(filepath.Join(inner, ".shortcuts"), map[string]any{})
	_, err := FindEntry([]string{"outer"}, inner, false, false)
	wantErr(t, err, "The project in "+outer+" has a 'outer', but only the nearest project, "+inner+", is used here.")
}

// py: test_stores.py::test_a_1x_project_folder_is_named
func TestHidingAndNotFound_AOneXProjectFolderIsNamed(t *testing.T) {
	e := testkit.New(t)
	old := e.Project(nil, testkit.Name("old"), testkit.Dirs(".shortcuts"))
	_, err := FindEntry([]string{"x"}, old, false, false)
	wantErr(t, err, filepath.Join(old, ".shortcuts")+" is a folder, which is how shortcut 1.x kept a project.")
}

func TestLookup_NamespacesAndNotFound(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "docker", map[string]any{"version": 2, "shortcuts": map[string]any{"where": inline("pwd"), "delete": inline("x")}})
	for _, c := range []struct {
		words []string
		want  string
	}{
		{[]string{"docker"}, "'docker' is a namespace; run one of its shortcuts: delete, where"},
		{[]string{"docker", "nope"}, "'nope' not found in namespace 'docker'. It has: delete, where"},
		{[]string{"podman", "list"}, "shortcut or namespace 'podman' not found. A global namespace is the file " +
			filepath.Join(e.Store, "imports", "podman.shortcut") + "."},
		{[]string{"podman"}, "shortcut 'podman' not found."},
	} {
		_, err := FindEntry(c.words, e.Home, false, false)
		if err == nil || err.Error() != c.want {
			t.Errorf("%v: got %v, want %q", c.words, err, c.want)
		}
	}
	f, err := FindEntry([]string{"docker", "where", "a", "b"}, e.Home, false, false)
	if err != nil || f.Name != "where" || f.Group.Name != "docker" || strings.Join(f.Rest, " ") != "a b" || f.Hides {
		t.Errorf("docker where a b: %+v %v", f, err)
	}
}

func TestLookup_AProjectNameHidesAGlobalOne(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "docker", map[string]any{"version": 2, "shortcuts": map[string]any{"ps": inline("echo global-ps"), "delete": inline("x")}})
	e.WriteStore(e.Store, map[string]any{"tool": inline("x")})
	proj := e.Project(map[string]any{"tool": inline("p")})
	e.WriteImport(filepath.Join(proj, ".shortcuts"), "docker", map[string]any{"version": 2, "shortcuts": map[string]any{"delete": inline("echo project-delete")}})

	f, err := FindEntry([]string{"docker", "delete"}, proj, false, false)
	if err != nil || !f.Hides || f.Group.Name != "docker" || !f.Group.IsProject() {
		t.Errorf("project namespace over a global one: %+v %v", f, err)
	}
	_, err = FindEntry([]string{"docker", "ps"}, proj, false, false)
	wantErr(t, err, "'ps' not found in namespace 'docker'. It has: delete. Your global 'docker' has it: shortcut r --global docker ps")
	if f, err = FindEntry([]string{"tool"}, proj, false, false); err != nil || !f.Hides {
		t.Errorf("project shortcut over a global one: %+v %v", f, err)
	}
	if f, err = FindEntry([]string{"tool"}, proj, true, false); err != nil || f.Hides || f.Group.IsProject() {
		t.Errorf("--global: %+v %v", f, err)
	}
}

func TestLookup_GlobalFlagPointsAtTheProjectsOwnName(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"build": inline("p")})
	_, err := FindEntry([]string{"build"}, proj, true, false)
	wantErr(t, err, "shortcut 'build' not found.")
	wantErr(t, err, "The project in "+proj+" has its own 'build': leave out --global to use it.")
	_, err = FindEntry([]string{"nothing"}, proj, true, false)
	if err == nil || strings.Contains(err.Error(), "has its own") {
		t.Errorf("a name the project lacks: %v", err)
	}
	_, err = FindEntry([]string{"build"}, e.Home, false, false)
	if err == nil || strings.Contains(err.Error(), "has its own") {
		t.Errorf("outside the project: %v", err)
	}
}

// strict (run) refuses what a broken project holds; the default (show, location, rm) reads as far as it can.
func TestLookup_StrictAndBrokenStores(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"outside": inline("echo outside")})
	broken := e.BrokenProject()
	registry := filepath.Join(broken, ".shortcuts")
	for _, name := range []string{"outside", "nothing"} {
		_, err := FindEntry([]string{name}, broken, false, true)
		wantErr(t, err, "corrupt registry "+registry)
		wantErr(t, err, ". Nothing runs inside this project until it's fixed; for your own: shortcut r --global "+name)
	}
	if f, err := FindEntry([]string{"outside"}, broken, true, true); err != nil || f.Group.IsProject() {
		t.Errorf("--global past a broken project: %+v %v", f, err)
	}
	if f, err := FindEntry([]string{"outside"}, broken, false, false); err != nil || f.Name != "outside" {
		t.Errorf("show reads past a broken project: %+v %v", f, err)
	}
	_, err := FindEntry([]string{"nothing"}, broken, false, false)
	wantErr(t, err, "shortcut 'nothing' not found.")

	// an unknown top-level field stops every name of the project, strictly; the file still reads
	proj := e.Project(map[string]any{"inside": inline("true")}, testkit.Name("p2"), testkit.Extra(map[string]any{"bogus": 1}))
	_, err = FindEntry([]string{"inside"}, proj, false, true)
	wantErr(t, err, filepath.Join(proj, ".shortcuts")+" has an unknown field: bogus. Nothing runs inside this project")
	if f, err := FindEntry([]string{"inside"}, proj, false, false); err != nil || f.Name != "inside" {
		t.Errorf("non-strict: %+v %v", f, err)
	}

	// a name that is both a shortcut and a namespace is a problem of that store only
	clash := e.Project(map[string]any{"docker": inline("echo shortcut"), "ok": inline("echo ok")}, testkit.Name("p3"))
	e.WriteImport(filepath.Join(clash, ".shortcuts"), "docker", map[string]any{"version": 2, "shortcuts": map[string]any{"ps": inline("x")}})
	for _, words := range [][]string{{"docker"}, {"docker", "ps"}, {"ok"}} {
		_, err := FindEntry(words, clash, false, true)
		wantErr(t, err, "'docker' is both a shortcut and a namespace")
	}
	if _, err := FindEntry([]string{"outside"}, clash, false, true); err != nil {
		t.Errorf("a name of the healthy store: %v", err)
	}
}

func TestLookup_ResolveNameTakesOneName(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"build": inline("echo b")})
	t.Chdir(e.Home)
	if r, err := ResolveName([]string{"build"}, false); err != nil || r.Entry.Name != "build" || r.RunCwd != pyos.Realpath(e.Home) {
		t.Errorf("one name: %v", err)
	}
	_, err := ResolveName([]string{"build", "an arg"}, false)
	if err == nil || err.Error() != "too many names: build 'an arg'" {
		t.Errorf("two words: %v", err)
	}
}
