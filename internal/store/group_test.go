package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// newGroup is a Group with an empty file, as LoadGroup gives for a missing one.
func newGroup() Group { return Group{Doc: platform.Doc{}} }

func TestGroup_ShortcutsOverlayAndSource(t *testing.T) {
	shared := doc(t, `{"shortcuts": {"a": {"inline": "1"}, "b": {"inline": "2"}, "c": {"inline": "3"}}}`)
	local := doc(t, `{"shortcuts": {"b": {"inline": "B"}, "d": {"inline": "4"}}}`)
	g := Group{
		Kind: KindProject, InProject: true, File: "/p/.shortcuts", StoreDir: "/p", LocalFile: "/p/.shortcuts.local",
		Doc: shared, Shared: ItemsOf(shared), Local: ItemsOf(local),
	}
	if got := strings.Join(g.Shortcuts().Names(), ","); got != "a,b,c,d" {
		t.Errorf("keys %s", got)
	}
	if it, _ := g.Shortcuts().Get("b"); string(it.Raw) != `{"inline": "B"}` {
		t.Error("b not overridden")
	}
	for name, want := range map[string]string{"a": "shared", "b": "overridden", "c": "shared", "d": "local"} {
		if got := string(g.Source(name)); got != want {
			t.Errorf("Source(%s) = %s, want %s", name, got, want)
		}
	}
	if g.DefiningFile("b") != "/p/.shortcuts.local" || g.DefiningFile("a") != "/p/.shortcuts" {
		t.Error("DefiningFile")
	}
	if len(newGroup().Shortcuts()) != 0 { // no local file (a namespace) and an empty file read as empty
		t.Error("empty group")
	}
	// a non-object "shortcuts" is empty, not a crash
	if d := doc(t, `{"shortcuts": []}`); len((Group{Doc: d, Shared: ItemsOf(d)}).Shortcuts()) != 0 {
		t.Error("shortcuts that is not an object")
	}
}

func TestGroup_Names(t *testing.T) {
	env := testkit.New(t)
	ns := Group{Kind: KindNamespace, Name: "docker", File: filepath.Join(env.Store, "imports", "docker.shortcut"), StoreDir: env.Store}
	if ns.Display("ps") != "docker ps" || ns.Label() != "docker" ||
		ns.ScriptBase() != filepath.Join(env.Store, "imports") || ns.ScriptsDir() != filepath.Join(env.Store, "imports", "scripts", "docker") {
		t.Errorf("%+v", ns)
	}
	global := Group{Kind: KindGlobal, File: filepath.Join(env.Store, "shortcuts.json"), StoreDir: env.Store}
	if global.InProject || global.Display("x") != "x" || global.ScriptBase() != env.Store ||
		global.ScriptsDir() != filepath.Join(env.Store, "scripts") {
		t.Errorf("%+v", global)
	}
	proj := Group{Kind: KindProject, InProject: true, File: filepath.Join(env.Tmp, "p", ".shortcuts"), StoreDir: filepath.Join(env.Tmp, "p")}
	if !proj.InProject || proj.Label() != proj.StoreDir {
		t.Errorf("%+v", proj)
	}
}

func TestGroup_KindFromLoaders(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{})
	if g := LoadGroup(filepath.Join(e.Store, "shortcuts.json"), e.Store, ""); g.Kind != KindGlobal || g.InProject {
		t.Errorf("global: %+v", g)
	}
	proj := e.Project(map[string]any{"x": testkit.Inline("x")})
	if g := LoadProjectGroup(proj); g.Kind != KindProject || !g.InProject {
		t.Errorf("project: %+v", g)
	}
	ns := filepath.Join(e.Home, "n.shortcut")
	e.WriteFile(ns, `{"version": 1, "shortcuts": {}}`, 0)
	e.WriteFile(filepath.Join(proj, ".shortcuts"), `{"version": 1, "shortcuts": {}, "imports": {"n": "`+ns+`"}}`, 0)
	e.WriteFile(filepath.Join(e.Store, "shortcuts.json"), `{"version": 1, "shortcuts": {}, "imports": {"n": "`+ns+`"}}`, 0)
	c := NewContext(proj, nil)
	if g, ok := c.Store(c.Project).Namespace("n"); !ok || g.Kind != KindNamespace || !g.InProject {
		t.Errorf("project namespace: %+v", g)
	}
	if g, ok := c.Store(e.Store).Namespace("n"); !ok || g.Kind != KindNamespace || g.InProject {
		t.Errorf("global namespace: %+v", g)
	}
}

func TestItem_TextShowsAWrongTypeAsWritten(t *testing.T) {
	items := ItemsOf(doc(t, `{"shortcuts": {"a": {"inline": "x", "description": 5, "cwd": null}, "b": {"inline": "x", "description": "d"}, "c": 7, "d": {"inline": "x", "description": ["a", "b"]}}}`))
	for _, c := range []struct{ name, field, want string }{
		{"a", "description", "5"}, {"a", "cwd", ""}, {"a", "inline", "x"}, {"a", "script", ""},
		{"b", "description", "d"}, {"c", "description", ""},
		{"d", "description", `["a", "b"]`},
	} {
		item, _ := items.Get(c.name)
		if got := item.Text(c.field); got != c.want {
			t.Errorf("%s.%s = %q, want %q", c.name, c.field, got, c.want)
		}
	}
	if c, _ := items.Get("c"); c.Entry != nil || c.Err == nil {
		t.Errorf("a non-object entry: %+v", c)
	}
}
