package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestStores_LoadProjectGroupErrors(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(map[string]any{"a": map[string]any{"inline": "1"}})
	env.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 1, "shortcuts": {}, "imports": {}}`, 0)
	g := LoadProjectGroup(proj)
	if want := filepath.Join(proj, ".shortcuts.local") + " can't have 'imports': namespaces belong in .shortcuts"; g.Error == nil || *g.Error != want {
		t.Errorf("%v", g.Error)
	}
	env.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{not json`, 0)
	if g = LoadProjectGroup(proj); g.Error == nil || !strings.HasPrefix(*g.Error, "corrupt registry "+filepath.Join(proj, ".shortcuts.local")+": ") {
		t.Errorf("%v", g.Error)
	}
	// the shared file's error comes first, and the shared data is whatever read
	env.WriteFile(filepath.Join(proj, ".shortcuts"), `{"version": 1, "shortcuts": {}, "extra": 1}`, 0)
	if g = LoadProjectGroup(proj); g.Error == nil || *g.Error != filepath.Join(proj, ".shortcuts")+" has an unknown field: extra" {
		t.Errorf("%v", g.Error)
	}
	// an absent .shortcuts is just empty
	only := env.Project(nil, testkit.Name("only"))
	env.WriteFile(filepath.Join(only, ".shortcuts.local"), `{"version": 1, "shortcuts": {"m": {"inline": "x"}}}`, 0)
	if g = LoadProjectGroup(only); g.Error != nil || len(g.Shortcuts()) != 1 {
		t.Errorf("%+v", g)
	}
}

func TestStores_EntryErrors(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(map[string]any{
		"ok":     map[string]any{"script": "bin/tool"},
		"gone":   map[string]any{"script": "bin/nope"},
		"escape": map[string]any{"script": "../x"},
		"bad":    map[string]any{"inline": "a", "script": "b"},
		"a b":    map[string]any{"inline": "x"},
		"isdir":  map[string]any{"script": "bin"},
	}, testkit.Files(map[string]any{"bin/tool": "x"}), testkit.Mode(0o755))
	g := NewContext(env.Home, nil).Store(proj).Main
	var got []string
	for _, e := range EntryErrors(g) {
		got = append(got, e[:min(len(e), 60)])
	}
	if len(got) != 5 {
		t.Fatalf("errors: %q", EntryErrors(g))
	}
	joined := strings.Join(EntryErrors(g), "\n")
	for _, want := range []string{
		"'gone' is not a valid entry: script not found: bin/nope",
		"path traversal detected: '../x' resolves to",
		"'bad' is not a valid entry: it needs exactly one of 'inline' and 'script'",
		"invalid shortcut name 'a b'.",
		"'isdir' is not a valid entry: script not found: bin",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "'ok'") {
		t.Errorf("a valid entry was reported:\n%s", joined)
	}
}
