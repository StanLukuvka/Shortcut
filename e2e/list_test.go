package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
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
		// names come out in byte order
		"B": testkit.Inline("true"), "a-1": testkit.Inline("true"), "a": testkit.Inline("true"), "10": testkit.Inline("true"),
	})

	if got := strings.Fields(e.RunWith(testkit.Opts{Cwd: proj}, "list", "--names").Stdout); !slices.Equal(got, []string{"10", "B", "a", "a-1", "glob", "penpot"}) {
		t.Errorf("global names: %v", got)
	}
	if got := strings.Fields(e.RunWith(testkit.Opts{Cwd: proj}, "list", "--names", ".").Stdout); !slices.Equal(got, []string{"local_cmd"}) {
		t.Errorf("project names: %v", got)
	}
	if got := e.Run("list", "--names", ".").Stdout; got != "" {
		t.Errorf("project names outside a project: %q", got)
	}
}

func TestStores_ListNamesOfANamespace(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{"b": testkit.Inline("true"), "a": testkit.Inline("true")}))
	if out := e.Run("list", "--names", "docker").Stdout; out != "a\nb\n" {
		t.Errorf("docker: %q", out)
	}
	if out := e.Run("list", "--names", "nothing").Stdout; out != "" {
		t.Errorf("nothing: %q", out)
	}
	// A project shortcut of that name is reached with the dot, so it changes nothing here
	proj := e.Project(map[string]any{"docker": testkit.Inline("true")})
	if out := e.RunWith(testkit.Opts{Cwd: proj}, "list", "--names", "docker").Stdout; out != "a\nb\n" {
		t.Errorf("in the project: %q", out)
	}
	if out := e.RunWith(testkit.Opts{Cwd: proj}, "list", "--names", ".").Stdout; out != "docker\n" {
		t.Errorf("project names: %q", out)
	}
}

func TestBrokenStores_ListWarnsAboutEveryEntryThatCannotRun(t *testing.T) {
	e := testkit.New(t)
	plain := e.WriteFile(filepath.Join(e.Tmp, "plain.sh"), "#!/bin/sh\n", 0o644)
	e.WriteStore(e.Store, map[string]any{
		"good":  testkit.Inline("true"),
		"gone":  map[string]any{"script": "scripts/none.sh"},
		"bad":   map[string]any{"inline": "true", "cwd": 123},
		"plain": map[string]any{"script": plain},
	})
	e.Folder("home/.shortcuts/scripts") // the folder of a script must be there; the script is what is missing
	e.WriteImport(e.Store, "ns", testkit.NS(map[string]any{"both": map[string]any{"inline": "true", "script": "a.sh"}}))
	res := e.Run("list")
	res.WantCode(t, 0)
	testkit.Contains(t, "stderr", res.Stderr, "script not found: scripts/none.sh")
	testkit.Contains(t, "stderr", res.Stderr, "'cwd' must be a string")
	testkit.Contains(t, "stderr", res.Stderr, "warning: ns: 'both'")
	testkit.Contains(t, "stderr", res.Stderr, "'plain': "+plain+" is not executable. Make it so: chmod +x "+plain)
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
	out := e.RunWith(testkit.Opts{Cwd: proj}, "list").Stdout
	testkit.Contains(t, "list", out, "Namespace docker ("+nsFile+"), run with: shortcut r . docker <name>:\n")
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
	proj := e.Project(map[string]any{"build": map[string]any{"description": "Proj build", "inline": "echo pb", "cwd": "."},
		"shared": map[string]any{"inline": "echo s", "description": "sh"}}, testkit.Extra(map[string]any{"description": "p"}))
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 1, "shortcuts": {"shared": {"inline": "echo local"}, "loc": {"inline": "l", "description": "mine"}}}`, 0)
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

// A description that is not a string, in a project file (refused) or in a global entry (shown), never panics.
func TestStores_ListSurvivesNonStringDescription(t *testing.T) {
	e := testkit.New(t)
	proj := filepath.Join(e.Home, "badlist")
	e.WriteStore(filepath.Join(proj, ".shortcuts"), map[string]any{
		"description": 5, "x": map[string]any{"inline": "true"}})
	e.WriteStore(e.Store, map[string]any{"g": map[string]any{"inline": "true", "description": 5}})
	res := e.RunWith(testkit.Opts{Cwd: proj}, "list")
	res.WantCode(t, 0)
	testkit.Matches(t, res.Stdout, `g +caller +5`)
}

// Far more than the pipe holds: head reads one line and leaves, shortcut ends by SIGPIPE (141) and prints nothing.
func TestList_APipeToHeadEndsQuietly(t *testing.T) {
	e := testkit.New(t)
	many := map[string]any{}
	for i := 0; i < 3000; i++ {
		many[fmt.Sprintf("%040d", i)] = map[string]any{"inline": "true", "description": "d"}
	}
	e.WriteStore(e.Store, many)
	res := e.Shell(testkit.Opts{Timeout: 30 * time.Second}, `shortcut list | head -n 1; echo "${PIPESTATUS[0]}" >&2`)
	if strings.Count(res.Stdout, "\n") != 1 || res.Stderr != "141\n" {
		t.Errorf("stdout %q stderr %q", res.Stdout, res.Stderr)
	}
}

func TestStores_AFifoScriptNeverBlocks(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"p": map[string]any{"script": "pipe"}, "q": testkit.Inline("true")}, testkit.Files(map[string]string{"pipe": "#!/bin/sh\n"}), testkit.Mode(0o755))
	os.Remove(filepath.Join(proj, "pipe"))
	if err := syscall.Mkfifo(filepath.Join(proj, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := in(proj)
	o.Timeout = 10 * time.Second
	for _, cmd := range [][]string{{"list"}, {"list", "--names"}, {"r", ".", "q"}} {
		e.RunWith(o, cmd...).WantCode(t, 0)
	}
	testkit.Contains(t, "list", e.RunWith(o, "list").Stderr, "script not found")
}

func TestStores_UnknownFieldsAreErrors(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, nil) // an empty store
	for _, c := range []struct {
		name, message string
		data          map[string]any
	}{
		{"top", "unknown field: owner", map[string]any{"version": 1, "owner": "x", "shortcuts": map[string]any{}}},
		{"entry", "unknown field: sandbox", testkit.NS(map[string]any{
			"a": map[string]any{"inline": "true", "sandbox": true}})},
		{"args", "unknown field: args", testkit.NS(map[string]any{
			"a": map[string]any{"inline": "true", "args": []any{map[string]any{"name": "x"}}}})},
	} {
		e.WriteImport(e.Store, c.name, c.data)
		testkit.Contains(t, c.name+" list", e.Run("list").Stderr, c.message)
	}
}

func TestStores_ShortcutsHomeMustBeAbsoluteAndTildeNeedsAHome(t *testing.T) {
	e := testkit.New(t)
	for _, c := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"SHORTCUTS_HOME": ""}, "SHORTCUTS_HOME must be an absolute path, not ''"},
		{map[string]string{"SHORTCUTS_HOME": "store"}, "SHORTCUTS_HOME must be an absolute path, not 'store'"},
		{map[string]string{"HOME": "relhome", "SHORTCUTS_HOME": "~/s"}, "HOME must be set to an absolute path, so '~' in '~/s' can't be expanded"},
	} { // testkit refuses an empty HOME, so "HOME unset" is the unit test of expandWith; a relative HOME gets here
		res := e.RunWith(testkit.Opts{Env: c.env}, "list")
		testkit.Eq(t, c.want+" exit", res.Code, 1)
		testkit.Contains(t, c.want, res.Stderr, "error: "+c.want)
	}
}

func TestProjectFiles_ImportsStayInTheSharedFile(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{})
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"),
		docText(t, map[string]any{"version": 1, "imports": map[string]any{"x": "x.shortcut"}, "shortcuts": map[string]any{}}), 0)
	testkit.Contains(t, "list", e.RunWith(in(proj), "list").Stderr, "can't have 'imports'")
}
