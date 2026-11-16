package model

import (
	"path/filepath"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/osutil"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestContext_StoreIsLoadedOnce(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"a": testkit.Inline("x")})
	c := NewContext(e.Home, nil)
	if _, has := c.Store(e.Store).Main.Shortcuts().Get("a"); !has {
		t.Fatal("a missing")
	}
	e.WriteStore(e.Store, map[string]any{"a": testkit.Inline("x"), "b": testkit.Inline("y")})
	if _, has := c.Store(e.Store).Main.Shortcuts().Get("b"); has {
		t.Error("a cached store was read again")
	}
	if _, has := c.ReadStore(e.Store).Main.Shortcuts().Get("b"); !has {
		t.Error("ReadStore did not see the new file")
	}
	if _, has := c.Store(e.Store).Main.Shortcuts().Get("b"); has {
		t.Error("ReadStore changed the cache")
	}
	if _, has := NewContext(e.Home, nil).Store(e.Store).Main.Shortcuts().Get("b"); !has {
		t.Error("a new Context did not see the new file")
	}
}

func TestContext_ProjectAndGlobalResolvedOnce(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"x": testkit.Inline("x")})
	c := NewContext(proj, nil)
	if c.Project != osutil.Realpath(proj) || c.Global != e.Store || !c.IsGlobal(e.Store) || c.IsGlobal(proj) {
		t.Errorf("project %q global %q", c.Project, c.Global)
	}
	if got := NewContext(filepath.Join(e.Home, "elsewhere"), nil).Project; got != "" {
		t.Errorf("no project: %q", got)
	}
}
