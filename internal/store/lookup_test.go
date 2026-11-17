package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestLookup_EachScopeReachesOnlyItsOwnStore(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"build": map[string]any{"description": "Global build", "inline": "echo global_build"}})
	proj := filepath.Join(e.Home, "my_project")
	e.WriteStore(filepath.Join(proj, ".shortcuts"), map[string]any{"build": map[string]any{"description": "Local project build", "inline": "echo local_build"}})

	// the same cwd, each scope its own store
	res, _, err := ResolveShortcut(NewContext(proj, nil), []string{"build"}, Lookup{})
	if err != nil || res.Group.StoreDir != mustReal(t, e.Store) || res.Entry.Description != "Global build" {
		t.Errorf("global scope inside the project: %v", err)
	}
	res, _, err = ResolveShortcut(NewContext(proj, nil), []string{"build"}, Lookup{Scope: ScopeProject})
	if err != nil || res.Group.StoreDir != mustReal(t, proj) || res.Entry.Description != "Local project build" {
		t.Errorf("project scope: %v", err)
	}
	// outside any project the dot is an error
	_, _, err = ResolveShortcut(NewContext(e.Home, nil), []string{"build"}, Lookup{Scope: ScopeProject})
	testkit.WantErr(t, err, "no project here: no .shortcuts or .shortcuts.local in this folder or its parents")
	_, _, err = ResolveShortcut(NewContext(e.Home, nil), []string{"check"}, Lookup{})
	testkit.WantErr(t, err, "shortcut 'check' not found")
}

func TestLookup_TheOtherScopeIsAHintNeverAFallback(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{"ps": testkit.Inline("echo global-ps")}))
	e.WriteStore(e.Store, map[string]any{"mine": testkit.Inline("x")})
	proj := e.Project(map[string]any{"tool": testkit.Inline("p")})
	e.WriteImport(filepath.Join(proj, ".shortcuts"), "terraform", testkit.NS(map[string]any{"plan": testkit.Inline("echo plan")}))
	e.WriteImport(filepath.Join(proj, ".shortcuts"), "docker", testkit.NS(map[string]any{"delete": testkit.Inline("echo project-delete")}))
	c := NewContext(proj, nil)
	for _, k := range []struct {
		words []string
		lk    Lookup
		want  string // "" = found
	}{
		{[]string{"tool"}, Lookup{Cmd: "r"}, "shortcut 'tool' not found. The project in " + proj + " has it: shortcut r . tool"},
		{[]string{"tool"}, Lookup{Cmd: "show"}, "The project in " + proj + " has it: shortcut show . tool"},
		{[]string{"tool", "a b"}, Lookup{Cmd: "r"}, "has it: shortcut r . tool 'a b'"},
		{[]string{"terraform", "plan"}, Lookup{Cmd: "r"}, "has it: shortcut r . terraform plan"},
		{[]string{"docker", "delete"}, Lookup{Cmd: "r"}, "'delete' not found in namespace 'docker'. It has: ps. The project in " + proj + " has it: shortcut r . docker delete"},
		{[]string{"mine"}, Lookup{Scope: ScopeProject, Cmd: "r"}, "shortcut 'mine' not found. Your global store has it: shortcut r mine"},
		{[]string{"docker", "ps"}, Lookup{Scope: ScopeProject, Cmd: "r"}, "'ps' not found in namespace 'docker'. It has: delete. Your global store has it: shortcut r docker ps"},
		{[]string{"nothing"}, Lookup{Cmd: "r"}, ""},
		{[]string{"tool"}, Lookup{}, ""}, // rm gives no hint
		{[]string{"docker", "ps"}, Lookup{}, "found"},
		{[]string{"docker", "delete"}, Lookup{Scope: ScopeProject}, "found"},
	} {
		f, err := FindEntry(c, k.words, k.lk)
		switch {
		case k.want == "found":
			if err != nil {
				t.Errorf("%v %+v: %v", k.words, k.lk, err)
			}
		case k.want == "":
			if err == nil || strings.Contains(err.Error(), "has it") || f.Item.Entry != nil {
				t.Errorf("%v %+v: want a plain not-found, got %v", k.words, k.lk, err)
			}
		default:
			if err == nil || !strings.Contains(err.Error(), k.want) {
				t.Errorf("%v %+v: got %v, want %q", k.words, k.lk, err, k.want)
			}
		}
	}
}

func TestLookup_ANameCannotBeADot(t *testing.T) {
	if err := ValidateName("."); err == nil {
		t.Error("'.' is a valid name, so a shortcut or namespace could be named like the project scope")
	}
}

func TestLookup_NotFound_ANameInAnOuterProjectIsPointedAt(t *testing.T) {
	e := testkit.New(t)
	outer := e.Project(map[string]any{"outer": testkit.Inline("echo outer")}, testkit.Name("outer"))
	inner := filepath.Join(outer, "vendor")
	e.WriteStore(filepath.Join(inner, ".shortcuts"), map[string]any{})
	_, err := FindEntry(NewContext(inner, nil), []string{"outer"}, Lookup{Scope: ScopeProject})
	testkit.WantErr(t, err, "The project in "+outer+" has a 'outer', but only the nearest project, "+inner+", is used here.")
}

func TestLookup_NotFound_AOneXProjectFolderGetsThePlainMessage(t *testing.T) {
	e := testkit.New(t)
	old := e.Project(nil, testkit.Name("old"), testkit.Dirs(".shortcuts"))
	_, err := FindEntry(NewContext(old, nil), []string{"x"}, Lookup{})
	if err == nil || err.Error() != "shortcut 'x' not found." {
		t.Errorf("got %v", err)
	}
}

func TestLookup_NamespacesAndNotFound(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{"where": testkit.Inline("pwd"), "delete": testkit.Inline("x")}))
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
		_, err := FindEntry(NewContext(e.Home, nil), c.words, Lookup{})
		if err == nil || err.Error() != c.want {
			t.Errorf("%v: got %v, want %q", c.words, err, c.want)
		}
	}
	f, err := FindEntry(NewContext(e.Home, nil), []string{"docker", "where", "a", "b"}, Lookup{})
	if err != nil || f.Name != "where" || f.Group.Name != "docker" || strings.Join(f.Rest, " ") != "a b" {
		t.Errorf("docker where a b: %+v %v", f, err)
	}
}

// strict (run) refuses what a broken project holds; the default (show, rm) reads as far as it can.
func TestLookup_StrictAndBrokenStores(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"outside": testkit.Inline("echo outside")})
	broken := e.BrokenProject()
	registry := filepath.Join(broken, ".shortcuts")
	for _, name := range []string{"outside", "nothing"} {
		_, err := FindEntry(NewContext(broken, nil), []string{name}, Lookup{Scope: ScopeProject, Strict: true})
		testkit.WantErr(t, err, "corrupt registry "+registry)
		testkit.WantErr(t, err, ". Nothing runs inside this project until it's fixed.")
	}
	if f, err := FindEntry(NewContext(broken, nil), []string{"outside"}, Lookup{Strict: true}); err != nil || f.Group.InProject {
		t.Errorf("a global lookup past a broken project: %+v %v", f, err)
	}
	_, err := FindEntry(NewContext(broken, nil), []string{"nothing"}, Lookup{Scope: ScopeProject})
	testkit.WantErr(t, err, "shortcut 'nothing' not found.")

	// an unknown top-level field stops every name of the project, strictly; the file still reads
	proj := e.Project(map[string]any{"inside": testkit.Inline("true")}, testkit.Name("p2"), testkit.Extra(map[string]any{"bogus": 1}))
	_, err = FindEntry(NewContext(proj, nil), []string{"inside"}, Lookup{Scope: ScopeProject, Strict: true})
	testkit.WantErr(t, err, filepath.Join(proj, ".shortcuts")+" has an unknown field: bogus. Nothing runs inside this project")
	if f, err := FindEntry(NewContext(proj, nil), []string{"inside"}, Lookup{Scope: ScopeProject}); err != nil || f.Name != "inside" {
		t.Errorf("non-strict: %+v %v", f, err)
	}

	// a name that is both a shortcut and a namespace is a problem of that store only
	clash := e.Project(map[string]any{"docker": testkit.Inline("echo shortcut"), "ok": testkit.Inline("echo ok")}, testkit.Name("p3"))
	e.WriteImport(filepath.Join(clash, ".shortcuts"), "docker", testkit.NS(map[string]any{"ps": testkit.Inline("x")}))
	for _, words := range [][]string{{"docker"}, {"docker", "ps"}, {"ok"}} {
		_, err := FindEntry(NewContext(clash, nil), words, Lookup{Scope: ScopeProject, Strict: true})
		testkit.WantErr(t, err, "'docker' is both a shortcut and a namespace")
	}
	if _, err := FindEntry(NewContext(clash, nil), []string{"outside"}, Lookup{Strict: true}); err != nil {
		t.Errorf("a name of the healthy store: %v", err)
	}
}

func TestLookup_ResolveNameTakesOneName(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"build": testkit.Inline("echo b")})
	t.Chdir(e.Home)
	cwd, _ := platform.Getwd()
	c := NewContext(cwd, nil)
	if r, err := ResolveName(c, []string{"build"}, Lookup{}); err != nil || r.Entry.Name != "build" || r.RunCwd != mustReal(t, e.Home) {
		t.Errorf("one name: %v", err)
	}
	_, err := ResolveName(c, []string{"build", "an arg"}, Lookup{})
	if err == nil || err.Error() != "too many names: build 'an arg'" {
		t.Errorf("two words: %v", err)
	}
}
