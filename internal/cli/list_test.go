package cli

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestStores_ListAndNames(t *testing.T) {
	e := testkit.New(t)
	proj := filepath.Join(e.Home, "list_proj")
	e.WriteStore(filepath.Join(proj, ".shortcuts"), map[string]any{
		"local_cmd": map[string]any{"description": "echo local", "inline": "echo local", "cwd": "."}})
	e.WriteStore(e.Store, map[string]any{
		"glob":   map[string]any{"description": "Global desc", "inline": "echo 1"},
		"penpot": map[string]any{"description": "Fixed desc", "inline": "echo 2", "cwd": e.Tmp},
	})

	// Nothing of an unapproved project shows, only how to review it
	res := e.RunWith(testkit.Opts{Cwd: proj}, "list")
	testkit.Contains(t, "list", res.Stdout, "aren't approved yet. Review them with: shortcut update")
	if strings.Contains(res.Stdout, "local_cmd") {
		t.Errorf("an unapproved project's name is listed:\n%s", res.Stdout)
	}
	if got := strings.Fields(e.RunWith(testkit.Opts{Cwd: proj}, "list", "--names").Stdout); !slices.Equal(got, []string{"glob", "penpot"}) {
		t.Errorf("names: %v", got)
	}

	e.Approve(filepath.Join(proj, ".shortcuts"))
	res = e.RunWith(testkit.Opts{Cwd: proj}, "list")
	res.WantCode(t, 0)
	testkit.Contains(t, "list", res.Stdout, "Project shortcuts")
	testkit.Matches(t, res.Stdout, `local_cmd +\. +shared +echo local`)
	testkit.Contains(t, "list", res.Stdout, "Global shortcuts")
	testkit.Matches(t, res.Stdout, `glob +caller +Global desc`)
	testkit.Matches(t, res.Stdout, `penpot +`+regexp.QuoteMeta(e.Tmp)+` +Fixed desc`)
	if got := strings.Fields(e.RunWith(testkit.Opts{Cwd: proj}, "list", "--names").Stdout); !slices.Equal(got, []string{"local_cmd", "glob", "penpot"}) {
		t.Errorf("names: %v", got)
	}
}

func TestStores_GroupDescriptionShowsOnceApproved(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "docker", map[string]any{"version": 1, "description": "Docker housekeeping",
		"shortcuts": map[string]any{"ps": testkit.Inline("echo ps")}})
	if out := e.Run("list").Stdout; strings.Contains(out, "Docker housekeeping") {
		t.Errorf("an unapproved namespace shows its description:\n%s", out)
	}
	// the update-diff line (the description is part of what update shows) is asserted in update_test.go
	runOK(t, e, testkit.Opts{}, "update", "docker", "-y")
	testkit.Contains(t, "list", e.Run("list").Stdout, "  Docker housekeeping\n")
}

func TestStores_ListNamesOfANamespace(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{"b": testkit.Inline("true"), "a": testkit.Inline("true")}))
	if out := e.Run("list", "--names", "docker").Stdout; out != "" {
		t.Errorf("unapproved: %q", out)
	}
	runOK(t, e, testkit.Opts{}, "update", "docker", "-y")
	if out := e.Run("list", "--names", "docker").Stdout; out != "a\nb\n" {
		t.Errorf("approved: %q", out)
	}
	if out := e.Run("list", "--names", "nothing").Stdout; out != "" {
		t.Errorf("nothing: %q", out)
	}
	// A project shortcut of that name is what `r docker` runs, so its arguments aren't names
	proj := e.Project(map[string]any{"docker": testkit.Inline("true")})
	e.Approve(filepath.Join(proj, ".shortcuts"))
	if out := e.RunWith(testkit.Opts{Cwd: proj}, "list", "--names", "docker").Stdout; out != "" {
		t.Errorf("in the project: %q", out)
	}
}

func TestBrokenStores_ListWarnsAboutEveryEntryThatCannotRun(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{
		"good": testkit.Inline("true"),
		"gone": map[string]any{"script": "scripts/none.sh"},
		"bad":  map[string]any{"inline": "true", "cwd": 123},
	})
	e.WriteImport(e.Store, "ns", testkit.NS(map[string]any{"both": map[string]any{"inline": "true", "script": "a.sh"}}))
	res := e.Run("list")
	res.WantCode(t, 0)
	testkit.Contains(t, "stderr", res.Stderr, "script not found: scripts/none.sh")
	testkit.Contains(t, "stderr", res.Stderr, "'cwd' must be a string")
	testkit.Contains(t, "stderr", res.Stderr, "warning: ns: 'both'")
	testkit.Contains(t, "stdout", res.Stdout, "good")
	if got := e.Run("list", "--names").Stderr; got != "" {
		t.Errorf("--names warned: %q", got)
	}
}

func TestStoreChecks_AnEmptyListSaysHowToStart(t *testing.T) {
	e := testkit.New(t)
	res := e.Run("list")
	if res.Code != 0 || res.Stdout != "" {
		t.Errorf("exit %d stdout %q", res.Code, res.Stdout)
	}
	testkit.Contains(t, "stderr", res.Stderr, "No shortcuts yet. Make one with: shortcut new hi")
}

// A project's entry over a global one is marked in both lists; the namespace form too.
func TestList_HidingIsMarked(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"penpot": testkit.Inline("echo global")})
	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{"ps": testkit.Inline("x"), "delete": testkit.Inline("x")}))
	runOK(t, e, testkit.Opts{}, "update", "docker", "-y")
	proj := e.Project(map[string]any{"penpot": map[string]any{"inline": "echo local", "cwd": "."}})
	nsFile := e.WriteImport(filepath.Join(proj, ".shortcuts"), "docker", testkit.NS(map[string]any{"delete": testkit.Inline("y")}))
	e.Approve(filepath.Join(proj, ".shortcuts"))
	out := e.RunWith(testkit.Opts{Cwd: proj}, "list").Stdout
	testkit.Contains(t, "list", out, "[hides global]")
	testkit.Contains(t, "list", out, "[hidden here by project]")
	testkit.Contains(t, "list", out, "Namespace docker ("+nsFile+")  [hides global]")
}

// The exact text of a list with a project, its .shortcuts.local, a namespace and a global store. The local entry replaces the shared one,
// so 'shared' has no description, and its row ends in the padding of the source column.
func TestList_FullListing(t *testing.T) {
	e := testkit.New(t)
	e.WriteRegistryRaw(e.Store, `{"version": 1, "description": "mine", "shortcuts": {
 "build": {"description": "Global build", "inline": "echo gb", "cwd": "/tmp"},
 "bad": {"inline": "x", "cwd": 5, "description": "bd"},
 "é": {"inline": "x", "description": "d"}}}`)
	e.WriteImport(e.Store, "docker", map[string]any{"version": 1, "description": "Docker stuff", "shortcuts": map[string]any{
		"ps": map[string]any{"inline": "docker ps", "description": "list"}}})
	runOK(t, e, testkit.Opts{}, "update", "docker", "-y")
	proj := e.Project(map[string]any{"build": map[string]any{"description": "Proj build", "inline": "echo pb", "cwd": "."},
		"shared": map[string]any{"inline": "echo s", "description": "sh"}}, testkit.Extra(map[string]any{"description": "p"}))
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 1, "shortcuts": {"shared": {"inline": "echo local"}, "loc": {"inline": "l", "description": "mine"}, "build": null}}`, 0)
	e.Approve(filepath.Join(proj, ".shortcuts"))
	res := e.RunWith(testkit.Opts{Cwd: proj}, "list")
	want := "Project shortcuts (" + proj + "):\n" +
		"  p\n" +
		"  build                .       hidden      Proj build  [hides global]\n" +
		"  loc                  caller  local       mine\n" +
		"  shared               caller  overridden  \n" +
		"\n" +
		"Global shortcuts:\n" +
		"  mine\n" +
		"  bad                  5       bd\n" +
		"  build                /tmp    Global build\n" +
		"  é                    caller  d\n" +
		"\n" +
		"Namespace docker (" + filepath.Join(e.Store, "imports", "docker.shortcut") + "):\n" +
		"  Docker stuff\n" +
		"  docker ps            caller  list\n"
	if res.Stdout != want {
		t.Errorf("got:\n%s\nwant:\n%s", res.Stdout, want)
	}
}

func TestList_NamespaceApprovalIsSingular(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{
		"ps": map[string]any{"inline": "docker ps", "description": "list"}}))
	testkit.Contains(t, "list", e.Run("list").Stdout, "Namespace docker ("+filepath.Join(e.Store, "imports", "docker.shortcut")+") isn't approved yet. Review it with")
	runOK(t, e, testkit.Opts{}, "update", "docker", "-y")
	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{
		"ps": map[string]any{"inline": "docker ps -a", "description": "list"}}))
	testkit.Contains(t, "changed", e.Run("list").Stdout, ") changed since you approved it. Review it with")
}

// A description that is not a string, in a project file (refused) or in a global entry (shown), never panics; it never panics.
func TestStores_ListSurvivesNonStringDescription(t *testing.T) {
	e := testkit.New(t)
	proj := filepath.Join(e.Home, "badlist")
	e.WriteStore(filepath.Join(proj, ".shortcuts"), map[string]any{
		"description": 5, "x": map[string]any{"inline": "true"}})
	e.WriteStore(e.Store, map[string]any{"g": map[string]any{"inline": "true", "description": 5}})
	res := e.RunWith(testkit.Opts{Cwd: proj}, "list")
	if strings.Contains(res.Stderr, "panic") || strings.Contains(res.Stderr, "goroutine") {
		t.Fatalf("list panicked:\n%s", res.Stderr)
	}
	testkit.Matches(t, res.Stdout, `g +caller +5`)
}
