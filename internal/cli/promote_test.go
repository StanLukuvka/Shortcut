package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

const (
	toolScript = "#!/bin/sh\necho \"tool in $PWD\"\n"
	warning    = "This runs the project's files as they are now, like typing the command there. " +
		"It isn't reviewed again when they change."
)

// toolProject is Python's tool_project: a project-only entry whose cwd is here.
func toolProject(e *testkit.Env, approved bool) string {
	opts := []testkit.ProjectOpt{testkit.Dirs("sub"), testkit.Files(map[string]any{"bin/tool": toolScript})}
	if approved {
		opts = append(opts, testkit.Approved())
	}
	return e.Project(map[string]any{"tool": map[string]any{"description": "a tool", "script": "bin/tool", "cwd": "sub"}}, opts...)
}

func removePath(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func noGlobalRegistry(t *testing.T, e *testkit.Env) {
	t.Helper()
	notExist(t, filepath.Join(e.Store, "shortcuts.json"))
}

// py: test_promote.py::test_an_exported_entry_promotes_without_its_export
func TestPromote_AnExportedEntryPromotesWithoutItsExport(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"tool": map[string]any{"script": "bin/tool", "export": true}},
		testkit.Files(map[string]any{"bin/tool": toolScript}), testkit.Approved())
	runOK(t, e, in(proj), "promote", "tool", "-y")
	eq(t, "global", e.LoadGlobal()["tool"], map[string]any{
		"description": "", "script": filepath.Join(proj, "bin", "tool"), "from": map[string]any{"source": proj, "name": "tool"}})
	elsewhere := e.Folder("elsewhere")
	eq(t, "run", e.RunWith(in(elsewhere), "r", "tool").Stdout, "tool in "+elsewhere+"\n")
}

// py: test_promote.py::test_an_entry_that_isnt_exported_needs_force
func TestPromote_AnEntryThatIsntExportedNeedsForce(t *testing.T) {
	e := testkit.New(t)
	proj := toolProject(e, true)
	res := e.RunWith(in(proj), "promote", "tool", "-y")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "error: 'tool' is marked project-only; promote anyway with --force\n")
	noGlobalRegistry(t, e)
}

// py: test_promote.py::test_force_points_at_the_checkout_and_copies_nothing
func TestPromote_ForcePointsAtTheCheckoutAndCopiesNothing(t *testing.T) {
	e := testkit.New(t)
	proj := toolProject(e, true)
	before := readFile(t, filepath.Join(proj, ".shortcuts"))

	res := runOK(t, e, in(proj), "promote", "tool", "--force", "-y")
	contains(t, "stdout", res.Stdout, warning)
	contains(t, "stderr", res.Stderr, "shortcut r --global tool runs yours")

	eq(t, "global", e.LoadGlobal()["tool"], map[string]any{
		"description": "a tool",
		"script":      filepath.Join(proj, "bin", "tool"),
		"cwd":         filepath.Join(proj, "sub"),
		"from":        map[string]any{"source": proj, "name": "tool"},
	})
	notExist(t, filepath.Join(e.Store, "scripts"))
	eq(t, "project file", readFile(t, filepath.Join(proj, ".shortcuts")), before)
	eq(t, "run", e.Run("r", "tool").Stdout, "tool in "+filepath.Join(proj, "sub")+"\n")
}

// py: test_promote.py::test_it_runs_the_project_script_as_it_is_now
func TestPromote_ItRunsTheProjectScriptAsItIsNow(t *testing.T) {
	e := testkit.New(t)
	proj := toolProject(e, true)
	runOK(t, e, in(proj), "promote", "tool", "--force", "-y")

	script := filepath.Join(proj, "bin", "tool")
	e.WriteFile(script, "#!/bin/sh\necho changed\n", 0)
	res := e.Run("r", "tool")
	eq(t, "changed", res.Stdout, "changed\n")

	removePath(t, script)
	res = e.Run("r", "tool")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "script file not found: "+script)
}

// py: test_promote.py::test_a_missing_run_directory_fails_loudly
func TestPromote_AMissingRunDirectoryFailsLoudly(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"here": map[string]any{"inline": "pwd", "cwd": "sub"}}, testkit.Dirs("sub"), testkit.Approved())
	runOK(t, e, in(proj), "promote", "here", "--force", "-y")
	removePath(t, filepath.Join(proj, "sub"))
	res := e.Run("r", "here")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "run directory does not exist: "+filepath.Join(proj, "sub"))
}

// py: test_promote.py::test_an_inline_entry_from_the_local_file_keeps_its_fields
func TestPromote_AnInlineEntryFromTheLocalFileKeepsItsFields(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(nil, testkit.Files(map[string]any{"mine.py": "print('mine')\n"}))
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), docText(t, map[string]any{"version": 2, "shortcuts": map[string]any{
		"hello": map[string]any{"description": "hi", "inline": "echo \"hi $1\"", "confirm": true, "cwd": "caller"},
		"abs":   map[string]any{"inline": "pwd", "cwd": "/"},
		"py":    map[string]any{"script": "mine.py"}}}), 0)
	e.Approve(filepath.Join(proj, ".shortcuts"))

	runOK(t, e, in(proj), "promote", "py", "--force", "-y")
	eq(t, "py script", e.LoadGlobal()["py"].(map[string]any)["script"], filepath.Join(proj, "mine.py"))

	runOK(t, e, in(proj), "promote", "hello", "--force", "-y")
	eq(t, "hello", e.LoadGlobal()["hello"], map[string]any{
		"description": "hi", "inline": "echo \"hi $1\"", "cwd": "caller", "confirm": true,
		"from": map[string]any{"source": proj, "name": "hello"}})

	runOK(t, e, in(proj), "promote", "abs", "--force", "-y")
	eq(t, "abs cwd", e.LoadGlobal()["abs"].(map[string]any)["cwd"], "/")
	notExist(t, filepath.Join(e.Store, "scripts"))
}

// py: test_promote.py::test_promote_needs_approval_first
func TestPromote_NeedsApprovalFirst(t *testing.T) {
	e := testkit.New(t)
	proj := toolProject(e, false)
	res := e.RunWith(in(proj), "promote", "tool", "--force", "-y")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "isn't approved yet")

	e.Approve(filepath.Join(proj, ".shortcuts"))
	e.WriteFile(filepath.Join(proj, "bin", "tool"), "#!/bin/sh\necho swapped\n", 0)
	res = e.RunWith(in(proj), "promote", "tool", "--force", "-y")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "changed since you approved it")
	noGlobalRegistry(t, e)
}

// py: test_promote.py::test_a_global_name_clash_needs_as
func TestPromote_AGlobalNameClashNeedsAs(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "tool", "--", "echo mine")
	proj := toolProject(e, true)
	before := readFile(t, filepath.Join(proj, ".shortcuts"))

	res := e.RunWith(in(proj), "promote", "tool", "--force", "-y")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "shortcut promote tool --as NEW")
	eq(t, "kept", e.LoadGlobal()["tool"].(map[string]any)["inline"], "echo mine")

	res = e.RunWith(in(proj), "promote", "tool", "--as", "tool", "--force", "-y")
	eq(t, "as exit", res.Code, 1)
	contains(t, "as stderr", res.Stderr, "you already have a global 'tool'")

	res = runOK(t, e, in(proj), "promote", "tool", "--as", "projtool", "--force", "-y")
	if strings.Contains(res.Stderr, "still the project's") {
		t.Errorf("note about the project's name: %q", res.Stderr)
	}
	eq(t, "from", e.LoadGlobal()["projtool"].(map[string]any)["from"], map[string]any{"source": proj, "name": "tool"})
	eq(t, "kept", e.LoadGlobal()["tool"].(map[string]any)["inline"], "echo mine")
	eq(t, "project file", readFile(t, filepath.Join(proj, ".shortcuts")), before)
}

// py: test_promote.py::test_without_a_terminal_it_needs_y
func TestPromote_WithoutATerminalItNeedsY(t *testing.T) {
	e := testkit.New(t)
	proj := toolProject(e, true)
	res := e.RunWith(in(proj), "promote", "tool", "--force")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "promote 'tool' in a terminal, or pass -y")
	noGlobalRegistry(t, e)
}

// py: test_promote.py::test_in_a_terminal_it_shows_the_entry_and_warning_then_asks
func TestPromote_InATerminalItShowsTheEntryAndWarningThenAsks(t *testing.T) {
	e := testkit.New(t)
	proj := toolProject(e, true)
	code, out := e.InTerminal(in(proj), []testkit.Key{{Wait: "[y/N]", Send: y("n\n")}}, "promote", "tool", "--force")
	eq(t, "n exit", code, 1)
	contains(t, "script", out, "\"script\": \""+filepath.Join(proj, "bin", "tool")+"\"")
	contains(t, "cwd", out, "\"cwd\": \""+filepath.Join(proj, "sub")+"\"")
	contains(t, "source", out, "\"source\": \""+proj+"\"")
	if strings.Index(out, warning) < 0 || strings.Index(out, warning) > strings.Index(out, "[y/N]") {
		t.Errorf("the warning is not before the question:\n%s", out)
	}
	contains(t, "n", out, "Aborted.")
	noGlobalRegistry(t, e)

	code, out = e.InTerminal(in(proj), []testkit.Key{{Wait: "[y/N]", Send: y("y\n")}}, "promote", "tool", "--force")
	if code != 0 {
		t.Errorf("y: exit %d\n%s", code, out)
	}
	if _, ok := e.LoadGlobal()["tool"]; !ok {
		t.Errorf("tool not promoted")
	}
}

// py: test_promote.py::test_only_a_project_shortcut_from_inside_a_project
func TestPromote_OnlyAProjectShortcutFromInsideAProject(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "mine", "--", "echo mine")
	res := e.RunWith(in(e.Folder("plain")), "promote", "mine", "--force", "-y")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "promote runs inside a project")

	proj := toolProject(e, true)
	res = e.RunWith(in(proj), "promote", "mine", "--force", "-y")
	eq(t, "inside exit", res.Code, 1)
	contains(t, "inside stderr", res.Stderr, "'mine' isn't a project shortcut here")
}

// py: test_promote.py::test_bash_completion_offers_promote_flags
func TestPromote_BashCompletionOffersPromoteFlags(t *testing.T) {
	e := testkit.New(t)
	if !slices.Contains(e.BashComplete("pro"), "promote") {
		t.Errorf("complete pro: no promote")
	}
	eq(t, "flags", e.BashComplete("promote", "-"), []string{"--as", "-y", "--force"})
}

var fromOrigin = map[string]any{"source": "/somewhere/proj", "name": "tool"}

// py: test_promote.py::test_show_prints_where_it_came_from
func TestFrom_ShowPrintsWhereItCameFrom(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"tool": map[string]any{"inline": "echo tool", "from": fromOrigin}})
	res := runOK(t, e, testkit.Opts{}, "show", "tool")
	contains(t, "show", res.Stdout, "From:        'tool' in /somewhere/proj\n")
	entry := decodeObject(t, runOK(t, e, testkit.Opts{}, "show", "--json", "tool").Stdout)["entry"].(map[string]any)
	eq(t, "json from", entry["from"], fromOrigin)
	runOK(t, e, testkit.Opts{}, "new", "plain", "--", "echo plain")
	if strings.Contains(e.Run("show", "plain").Stdout, "From:") {
		t.Errorf("a plain entry shows a From line")
	}
}

// py: test_promote.py::test_from_is_refused_in_a_project_and_a_namespace
func TestFrom_IsRefusedInAProjectAndANamespace(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"tool": map[string]any{"inline": "echo tool", "from": fromOrigin}})
	res := e.RunWith(in(proj), "update", "-y")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "'tool' is not a valid entry: 'from' is only for the global store's shortcuts.json")

	local := e.Project(nil, testkit.Name("local"))
	e.WriteFile(filepath.Join(local, ".shortcuts.local"), docText(t, map[string]any{"version": 2, "shortcuts": map[string]any{
		"tool": map[string]any{"inline": "echo tool", "from": fromOrigin}}}), 0)
	contains(t, "local", e.RunWith(in(local), "update", "-y").Stderr, "'from' is only for")

	e.WriteImport(e.Store, "ns", map[string]any{"version": 2, "shortcuts": map[string]any{
		"tool": map[string]any{"inline": "echo tool", "from": fromOrigin}}})
	res = e.Run("update", "ns", "-y")
	eq(t, "ns exit", res.Code, 1)
	contains(t, "ns", res.Stderr, "'from' is only for")
}

// py: test_promote.py::test_from_must_be_source_and_name
func TestFrom_MustBeSourceAndName(t *testing.T) {
	e := testkit.New(t)
	for _, bad := range []any{"proj", map[string]any{"source": "/p"}, map[string]any{"source": "/p", "name": 1},
		map[string]any{"source": "/p", "name": "t", "x": "y"}} {
		e.WriteStore(e.Store, map[string]any{"tool": map[string]any{"inline": "echo tool", "from": bad}})
		res := e.Run("r", "tool")
		eq(t, "exit", res.Code, 1)
		contains(t, "stderr", res.Stderr, "'from' must be an object with a string 'source' and 'name'")
	}
}

// py: test_promote.py::test_origin_is_not_a_json_field
func TestFrom_OriginIsNotAJSONField(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"tool": map[string]any{"inline": "echo tool", "origin": fromOrigin}})
	contains(t, "stderr", e.Run("r", "tool").Stderr, "unknown field: origin")
}
