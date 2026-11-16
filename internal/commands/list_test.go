package commands

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

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
	if got := e.RunWith(testkit.Opts{Cwd: proj}, "list", "--names", ".").Stdout; got != "" {
		t.Errorf("project names before approval: %q", got)
	}

	e.Approve(filepath.Join(proj, ".shortcuts"))
	res = e.RunWith(testkit.Opts{Cwd: proj}, "list")
	res.WantCode(t, 0)
	testkit.Contains(t, "list", res.Stdout, "Project shortcuts")
	testkit.Matches(t, res.Stdout, `local_cmd +\. +shared +echo local`)
	testkit.Contains(t, "list", res.Stdout, "Global shortcuts")
	testkit.Matches(t, res.Stdout, `glob +caller +Global desc`)
	testkit.Matches(t, res.Stdout, `penpot +`+regexp.QuoteMeta(e.Tmp)+` +Fixed desc`)
	if got := strings.Fields(e.RunWith(testkit.Opts{Cwd: proj}, "list", "--names").Stdout); !slices.Equal(got, []string{"glob", "penpot"}) {
		t.Errorf("global names: %v", got)
	}
	if got := strings.Fields(e.RunWith(testkit.Opts{Cwd: proj}, "list", "--names", ".").Stdout); !slices.Equal(got, []string{"local_cmd"}) {
		t.Errorf("project names: %v", got)
	}
	if got := e.Run("list", "--names", ".").Stdout; got != "" {
		t.Errorf("project names outside a project: %q", got)
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
	// A project shortcut of that name is reached with the dot, so it changes nothing here
	proj := e.Project(map[string]any{"docker": testkit.Inline("true")})
	e.Approve(filepath.Join(proj, ".shortcuts"))
	if out := e.RunWith(testkit.Opts{Cwd: proj}, "list", "--names", "docker").Stdout; out != "a\nb\n" {
		t.Errorf("in the project: %q", out)
	}
	if out := e.RunWith(testkit.Opts{Cwd: proj}, "list", "--names", ".").Stdout; out != "docker\n" {
		t.Errorf("project names: %q", out)
	}
}

func TestBrokenStores_ListWarnsAboutAScriptThatIsNotExecutable(t *testing.T) {
	e := testkit.New(t)
	script := e.WriteFile(filepath.Join(e.Tmp, "plain.sh"), "#!/bin/sh\n", 0o644)
	e.WriteStore(e.Store, map[string]any{"plain": map[string]any{"script": script}})
	res := e.Run("list")
	res.WantCode(t, 0)
	testkit.Contains(t, "stderr", res.Stderr, "warning: ")
	testkit.Contains(t, "stderr", res.Stderr, "'plain': "+script+" is not executable. Make it so: chmod +x "+script)
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

// A project's shortcuts and namespaces are listed with the command that reaches them: the dot.
func TestList_ProjectSectionsSayTheyAreReachedWithTheDot(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"penpot": testkit.Inline("echo global")})
	proj := e.Project(map[string]any{"penpot": map[string]any{"inline": "echo local", "cwd": "."}})
	nsFile := e.WriteImport(filepath.Join(proj, ".shortcuts"), "docker", testkit.NS(map[string]any{"delete": testkit.Inline("y")}))
	e.Approve(filepath.Join(proj, ".shortcuts"))
	out := e.RunWith(testkit.Opts{Cwd: proj}, "list").Stdout
	testkit.Contains(t, "list", out, "Project shortcuts ("+proj+"), run with: shortcut r . <name>:\n")
	testkit.Contains(t, "list", out, "Namespace docker ("+nsFile+"), run with: shortcut r . docker <name>:\n")
	testkit.Contains(t, "list", out, "Global shortcuts:\n")
	if strings.Contains(out, "hides") || strings.Contains(out, "hidden") {
		t.Errorf("list marks hiding:\n%s", out)
	}
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
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 1, "shortcuts": {"shared": {"inline": "echo local"}, "loc": {"inline": "l", "description": "mine"}}}`, 0)
	e.Approve(filepath.Join(proj, ".shortcuts"))
	res := e.RunWith(testkit.Opts{Cwd: proj}, "list")
	want := "Project shortcuts (" + proj + "), run with: shortcut r . <name>:\n" +
		"  p\n" +
		"  build                .       shared      Proj build\n" +
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

// A description that is not a string, in a project file (refused) or in a global entry (shown), never panics.
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

// manyEntries is n inline shortcuts with 40-character names.
func manyEntries(n int) map[string]any {
	m := map[string]any{}
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("%040d", i)] = map[string]any{"inline": "true", "description": "d"}
	}
	return m
}

// Far more than the pipe holds: head reads one line and leaves, shortcut ends by SIGPIPE (141) and prints nothing.
func TestList_APipeToHeadEndsQuietly(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, manyEntries(3000))
	res := e.Shell(testkit.Opts{Timeout: 30 * time.Second}, `shortcut list | head -n 1; echo "${PIPESTATUS[0]}" >&2`)
	if strings.Count(res.Stdout, "\n") != 1 || res.Stderr != "141\n" {
		t.Errorf("stdout %q stderr %q", res.Stdout, res.Stderr)
	}
}

// stdout is not buffered: a line to stdout and a note to stderr reach one pipe in the order they were written.
func TestNew_StdoutAndStderrKeepTheirOrderInOnePipe(t *testing.T) {
	e := testkit.New(t)
	res := e.Shell(testkit.Opts{}, `shortcut new ord -- true 2>&1 | cat`)
	testkit.Eq(t, "output", res.Stdout, "note: 'ord' won't see any arguments you give it; add \"$@\" to pass them on\nCreated inline shortcut 'ord'.\n")
}
