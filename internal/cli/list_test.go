// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-2). Don't hand-edit without checking the plan.

package cli

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func inline(text string) map[string]any { return map[string]any{"inline": text} }

func matches(t *testing.T, text, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("no match for %s in:\n%s", pattern, text)
	}
}

func contains(t *testing.T, what, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Errorf("%s: %q lacks %q", what, text, want)
	}
}

// py: test_stores.py::test_list_and_names
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
	contains(t, "list", res.Stdout, "aren't approved yet. Review them with: shortcut update")
	if strings.Contains(res.Stdout, "local_cmd") {
		t.Errorf("an unapproved project's name is listed:\n%s", res.Stdout)
	}
	if got := strings.Fields(e.RunWith(testkit.Opts{Cwd: proj}, "list", "--names").Stdout); !slices.Equal(got, []string{"glob", "penpot"}) {
		t.Errorf("names: %v", got)
	}

	e.Approve(filepath.Join(proj, ".shortcuts"))
	res = e.RunWith(testkit.Opts{Cwd: proj}, "list")
	if res.Code != 0 {
		t.Errorf("exit %d", res.Code)
	}
	contains(t, "list", res.Stdout, "Project shortcuts")
	matches(t, res.Stdout, `local_cmd +\. +shared +echo local`)
	contains(t, "list", res.Stdout, "Global shortcuts")
	matches(t, res.Stdout, `glob +caller +Global desc`)
	matches(t, res.Stdout, `penpot +`+regexp.QuoteMeta(e.Tmp)+` +Fixed desc`)
	if got := strings.Fields(e.RunWith(testkit.Opts{Cwd: proj}, "list", "--names").Stdout); !slices.Equal(got, []string{"local_cmd", "glob", "penpot"}) {
		t.Errorf("names: %v", got)
	}
}

// py: test_stores.py::test_group_description_shows_once_approved
func TestStores_GroupDescriptionShowsOnceApproved(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "docker", map[string]any{"version": 2, "description": "Docker housekeeping",
		"shortcuts": map[string]any{"ps": inline("echo ps")}})
	if out := e.Run("list").Stdout; strings.Contains(out, "Docker housekeeping") {
		t.Errorf("an unapproved namespace shows its description:\n%s", out)
	}
	// Python's update-diff line (the description is part of what update shows) is asserted in update_test.go
	runOK(t, e, testkit.Opts{}, "update", "docker", "-y")
	contains(t, "list", e.Run("list").Stdout, "  Docker housekeeping\n")
}

// py: test_stores.py::test_list_names_of_a_namespace
func TestStores_ListNamesOfANamespace(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "docker", map[string]any{"version": 2, "shortcuts": map[string]any{"b": inline("true"), "a": inline("true")}})
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
	proj := e.Project(map[string]any{"docker": inline("true")})
	e.Approve(filepath.Join(proj, ".shortcuts"))
	if out := e.RunWith(testkit.Opts{Cwd: proj}, "list", "--names", "docker").Stdout; out != "" {
		t.Errorf("in the project: %q", out)
	}
}

// py: test_stores.py::test_list_warns_about_every_entry_that_cannot_run
func TestBrokenStores_ListWarnsAboutEveryEntryThatCannotRun(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{
		"good": inline("true"),
		"gone": map[string]any{"script": "scripts/none.sh"},
		"bad":  map[string]any{"inline": "true", "cwd": 123},
	})
	e.WriteImport(e.Store, "ns", map[string]any{"version": 2, "shortcuts": map[string]any{"both": map[string]any{"inline": "true", "script": "a.sh"}}})
	res := e.Run("list")
	if res.Code != 0 {
		t.Errorf("exit %d: %s", res.Code, res.Stderr)
	}
	contains(t, "stderr", res.Stderr, "script not found: scripts/none.sh")
	contains(t, "stderr", res.Stderr, "'cwd' must be a string")
	contains(t, "stderr", res.Stderr, "warning: ns: 'both'")
	contains(t, "stdout", res.Stdout, "good")
	if got := e.Run("list", "--names").Stderr; got != "" {
		t.Errorf("--names warned: %q", got)
	}
}

// py: test_stores.py::test_an_empty_list_says_how_to_start
func TestStoreChecks_AnEmptyListSaysHowToStart(t *testing.T) {
	e := testkit.New(t)
	res := e.Run("list")
	if res.Code != 0 || res.Stdout != "" {
		t.Errorf("exit %d stdout %q", res.Code, res.Stdout)
	}
	contains(t, "stderr", res.Stderr, "No shortcuts yet. Make one with: shortcut new hi")
}

// A project's entry over a global one is marked in both lists; the namespace form too (py: the list lines of
// test_local_shortcut_hiding_global_warns and test_a_project_namespace_over_a_global_one_is_announced, whose other
// lines are `new` (P-4), `update` and `run`).
func TestList_HidingIsMarked(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"penpot": inline("echo global")})
	e.WriteImport(e.Store, "docker", map[string]any{"version": 2, "shortcuts": map[string]any{"ps": inline("x"), "delete": inline("x")}})
	runOK(t, e, testkit.Opts{}, "update", "docker", "-y")
	proj := e.Project(map[string]any{"penpot": map[string]any{"inline": "echo local", "cwd": "."}})
	nsFile := e.WriteImport(filepath.Join(proj, ".shortcuts"), "docker", map[string]any{"version": 2, "shortcuts": map[string]any{"delete": inline("y")}})
	e.Approve(filepath.Join(proj, ".shortcuts"))
	out := e.RunWith(testkit.Opts{Cwd: proj}, "list").Stdout
	contains(t, "list", out, "[hides global]")
	contains(t, "list", out, "[hidden here by project]")
	contains(t, "list", out, "Namespace docker ("+nsFile+")  [hides global]")
}

// The exact text of a list with a project, its .shortcuts.local, a namespace and a global store, as the Python
// version printed it (checked against it by hand: the same bytes). The local entry replaces the shared one,
// so 'shared' has no description, and its row ends in the padding of the source column.
func TestList_FullListingMatchesPython(t *testing.T) {
	e := testkit.New(t)
	e.WriteRegistryRaw(e.Store, `{"version": 2, "description": "mine", "shortcuts": {
 "build": {"description": "Global build", "inline": "echo gb", "cwd": "/tmp"},
 "bad": {"inline": "x", "cwd": 5, "description": "bd"},
 "é": {"inline": "x", "description": "d"}}}`)
	e.WriteImport(e.Store, "docker", map[string]any{"version": 2, "description": "Docker stuff", "shortcuts": map[string]any{
		"ps": map[string]any{"inline": "docker ps", "description": "list"}}})
	runOK(t, e, testkit.Opts{}, "update", "docker", "-y")
	proj := e.Project(map[string]any{"build": map[string]any{"description": "Proj build", "inline": "echo pb", "cwd": "."},
		"shared": map[string]any{"inline": "echo s", "description": "sh"}}, testkit.Extra(map[string]any{"description": "p"}))
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 2, "shortcuts": {"shared": {"inline": "echo local"}, "loc": {"inline": "l", "description": "mine"}, "build": null}}`, 0)
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
