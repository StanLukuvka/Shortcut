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

// toolProject is a project-only entry whose cwd is here.
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
	testkit.NotExist(t, filepath.Join(e.Store, "shortcuts.json"))
}

func TestPromote_AnExportedEntryPromotesWithoutItsExport(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"tool": map[string]any{"script": "bin/tool", "export": true}},
		testkit.Files(map[string]any{"bin/tool": toolScript}), testkit.Approved())
	runOK(t, e, in(proj), "promote", "tool", "-y")
	testkit.Eq(t, "global", e.LoadGlobal()["tool"], map[string]any{
		"description": "", "script": filepath.Join(proj, "bin", "tool"), "from": map[string]any{"source": proj, "name": "tool"}})
	elsewhere := e.Folder("elsewhere")
	testkit.Eq(t, "run", e.RunWith(in(elsewhere), "r", "tool").Stdout, "tool in "+elsewhere+"\n")
}

func TestPromote_AnEntryThatIsntExportedNeedsForce(t *testing.T) {
	e := testkit.New(t)
	proj := toolProject(e, true)
	res := e.RunWith(in(proj), "promote", "tool", "-y")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "error: 'tool' is marked project-only; promote anyway with --force\n")
	noGlobalRegistry(t, e)
}

func TestPromote_ForcePointsAtTheCheckoutAndCopiesNothing(t *testing.T) {
	e := testkit.New(t)
	proj := toolProject(e, true)
	before := testkit.ReadFile(t, filepath.Join(proj, ".shortcuts"))

	res := runOK(t, e, in(proj), "promote", "tool", "--force", "-y")
	testkit.Contains(t, "stdout", res.Stdout, warning)
	testkit.Contains(t, "stderr", res.Stderr, "shortcut r --global tool runs yours")

	testkit.Eq(t, "global", e.LoadGlobal()["tool"], map[string]any{
		"description": "a tool",
		"script":      filepath.Join(proj, "bin", "tool"),
		"cwd":         filepath.Join(proj, "sub"),
		"from":        map[string]any{"source": proj, "name": "tool"},
	})
	testkit.NotExist(t, filepath.Join(e.Store, "scripts"))
	testkit.Eq(t, "project file", testkit.ReadFile(t, filepath.Join(proj, ".shortcuts")), before)
	testkit.Eq(t, "run", e.Run("r", "tool").Stdout, "tool in "+filepath.Join(proj, "sub")+"\n")
}

func TestPromote_ItRunsTheProjectScriptAsItIsNow(t *testing.T) {
	e := testkit.New(t)
	proj := toolProject(e, true)
	runOK(t, e, in(proj), "promote", "tool", "--force", "-y")

	script := filepath.Join(proj, "bin", "tool")
	e.WriteFile(script, "#!/bin/sh\necho changed\n", 0)
	res := e.Run("r", "tool")
	testkit.Eq(t, "changed", res.Stdout, "changed\n")

	removePath(t, script)
	res = e.Run("r", "tool")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "script file not found: "+script)
}

func TestPromote_AMissingRunDirectoryFailsLoudly(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"here": map[string]any{"inline": "pwd", "cwd": "sub"}}, testkit.Dirs("sub"), testkit.Approved())
	runOK(t, e, in(proj), "promote", "here", "--force", "-y")
	removePath(t, filepath.Join(proj, "sub"))
	res := e.Run("r", "here")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "run directory does not exist: "+filepath.Join(proj, "sub"))
}

func TestPromote_AnInlineEntryFromTheLocalFileKeepsItsFields(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(nil, testkit.Files(map[string]any{"mine.py": "print('mine')\n"}))
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), docText(t, testkit.NS(map[string]any{
		"hello": map[string]any{"description": "hi", "inline": "echo \"hi $1\"", "confirm": true, "cwd": "caller"},
		"abs":   map[string]any{"inline": "pwd", "cwd": "/"},
		"py":    map[string]any{"script": "mine.py"}})), 0)
	e.Approve(filepath.Join(proj, ".shortcuts"))

	runOK(t, e, in(proj), "promote", "py", "--force", "-y")
	testkit.Eq(t, "py script", e.LoadGlobal()["py"].(map[string]any)["script"], filepath.Join(proj, "mine.py"))

	runOK(t, e, in(proj), "promote", "hello", "--force", "-y")
	testkit.Eq(t, "hello", e.LoadGlobal()["hello"], map[string]any{
		"description": "hi", "inline": "echo \"hi $1\"", "cwd": "caller", "confirm": true,
		"from": map[string]any{"source": proj, "name": "hello"}})

	runOK(t, e, in(proj), "promote", "abs", "--force", "-y")
	testkit.Eq(t, "abs cwd", e.LoadGlobal()["abs"].(map[string]any)["cwd"], "/")
	testkit.NotExist(t, filepath.Join(e.Store, "scripts"))
}

func TestPromote_NeedsApprovalFirst(t *testing.T) {
	e := testkit.New(t)
	proj := toolProject(e, false)
	res := e.RunWith(in(proj), "promote", "tool", "--force", "-y")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "isn't approved yet")

	e.Approve(filepath.Join(proj, ".shortcuts"))
	e.WriteFile(filepath.Join(proj, "bin", "tool"), "#!/bin/sh\necho swapped\n", 0)
	res = e.RunWith(in(proj), "promote", "tool", "--force", "-y")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "changed since you approved it")
	noGlobalRegistry(t, e)
}

func TestPromote_AGlobalNameClashNeedsAs(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "tool", "--", "echo mine")
	proj := toolProject(e, true)
	before := testkit.ReadFile(t, filepath.Join(proj, ".shortcuts"))

	res := e.RunWith(in(proj), "promote", "tool", "--force", "-y")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "shortcut promote tool --as NEW")
	testkit.Eq(t, "kept", e.LoadGlobal()["tool"].(map[string]any)["inline"], "echo mine")

	res = e.RunWith(in(proj), "promote", "tool", "--as", "tool", "--force", "-y")
	testkit.Eq(t, "as exit", res.Code, 1)
	testkit.Contains(t, "as stderr", res.Stderr, "you already have a global 'tool'")

	res = runOK(t, e, in(proj), "promote", "tool", "--as", "projtool", "--force", "-y")
	if strings.Contains(res.Stderr, "still the project's") {
		t.Errorf("note about the project's name: %q", res.Stderr)
	}
	testkit.Eq(t, "from", e.LoadGlobal()["projtool"].(map[string]any)["from"], map[string]any{"source": proj, "name": "tool"})
	testkit.Eq(t, "kept", e.LoadGlobal()["tool"].(map[string]any)["inline"], "echo mine")
	testkit.Eq(t, "project file", testkit.ReadFile(t, filepath.Join(proj, ".shortcuts")), before)
}

func TestPromote_WithoutATerminalItNeedsY(t *testing.T) {
	e := testkit.New(t)
	proj := toolProject(e, true)
	res := e.RunWith(in(proj), "promote", "tool", "--force")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "promote 'tool' in a terminal, or pass -y")
	noGlobalRegistry(t, e)
}

func TestPromote_InATerminalItShowsTheEntryAndWarningThenAsks(t *testing.T) {
	e := testkit.New(t)
	proj := toolProject(e, true)
	code, out := e.InTerminal(in(proj), []testkit.Key{{Wait: "[y/N]", Send: y("n\n")}}, "promote", "tool", "--force")
	testkit.Eq(t, "n exit", code, 1)
	testkit.Contains(t, "script", out, "\"script\": \""+filepath.Join(proj, "bin", "tool")+"\"")
	testkit.Contains(t, "cwd", out, "\"cwd\": \""+filepath.Join(proj, "sub")+"\"")
	testkit.Contains(t, "source", out, "\"source\": \""+proj+"\"")
	if strings.Index(out, warning) < 0 || strings.Index(out, warning) > strings.Index(out, "[y/N]") {
		t.Errorf("the warning is not before the question:\n%s", out)
	}
	testkit.Contains(t, "n", out, "Aborted.")
	noGlobalRegistry(t, e)

	code, out = e.InTerminal(in(proj), []testkit.Key{{Wait: "[y/N]", Send: y("y\n")}}, "promote", "tool", "--force")
	if code != 0 {
		t.Errorf("y: exit %d\n%s", code, out)
	}
	if _, ok := e.LoadGlobal()["tool"]; !ok {
		t.Errorf("tool not promoted")
	}
}

func TestPromote_OnlyAProjectShortcutFromInsideAProject(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "mine", "--", "echo mine")
	res := e.RunWith(in(e.Folder("plain")), "promote", "mine", "--force", "-y")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "promote runs inside a project")

	proj := toolProject(e, true)
	res = e.RunWith(in(proj), "promote", "mine", "--force", "-y")
	testkit.Eq(t, "inside exit", res.Code, 1)
	testkit.Contains(t, "inside stderr", res.Stderr, "'mine' isn't a project shortcut here")
}

func TestPromote_BashCompletionOffersPromoteFlags(t *testing.T) {
	e := testkit.New(t)
	if !slices.Contains(e.BashComplete("pro"), "promote") {
		t.Errorf("complete pro: no promote")
	}
	testkit.Eq(t, "flags", e.BashComplete("promote", "-"), []string{"--as", "-y", "--force"})
}

var fromOrigin = map[string]any{"source": "/somewhere/proj", "name": "tool"}

func TestFrom_ShowPrintsWhereItCameFrom(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"tool": map[string]any{"inline": "echo tool", "from": fromOrigin}})
	res := runOK(t, e, testkit.Opts{}, "show", "tool")
	testkit.Contains(t, "show", res.Stdout, "From:        'tool' in /somewhere/proj\n")
	entry := decodeObject(t, runOK(t, e, testkit.Opts{}, "show", "--json", "tool").Stdout)["entry"].(map[string]any)
	testkit.Eq(t, "json from", entry["from"], fromOrigin)
	runOK(t, e, testkit.Opts{}, "new", "plain", "--", "echo plain")
	if strings.Contains(e.Run("show", "plain").Stdout, "From:") {
		t.Errorf("a plain entry shows a From line")
	}
}

func TestFrom_IsRefusedInAProjectAndANamespace(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"tool": map[string]any{"inline": "echo tool", "from": fromOrigin}})
	res := e.RunWith(in(proj), "update", ".", "-y")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "'tool' is not a valid entry: 'from' is only for the global store's shortcuts.json")

	local := e.Project(nil, testkit.Name("local"))
	e.WriteFile(filepath.Join(local, ".shortcuts.local"), docText(t, testkit.NS(map[string]any{
		"tool": map[string]any{"inline": "echo tool", "from": fromOrigin}})), 0)
	testkit.Contains(t, "local", e.RunWith(in(local), "update", "-y").Stderr, "'from' is only for")

	e.WriteImport(e.Store, "ns", testkit.NS(map[string]any{
		"tool": map[string]any{"inline": "echo tool", "from": fromOrigin}}))
	res = e.Run("update", "ns", "-y")
	testkit.Eq(t, "ns exit", res.Code, 1)
	testkit.Contains(t, "ns", res.Stderr, "'from' is only for")
}

func TestFrom_MustBeSourceAndName(t *testing.T) {
	e := testkit.New(t)
	for _, bad := range []any{"proj", map[string]any{"source": "/p"}, map[string]any{"source": "/p", "name": 1},
		map[string]any{"source": "/p", "name": "t", "x": "y"}} {
		e.WriteStore(e.Store, map[string]any{"tool": map[string]any{"inline": "echo tool", "from": bad}})
		res := e.Run("r", "tool")
		res.WantCode(t, 1)
		testkit.Contains(t, "stderr", res.Stderr, "'from' must be an object with a string 'source' and 'name'")
	}
}

func TestFrom_OriginIsNotAJSONField(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"tool": map[string]any{"inline": "echo tool", "origin": fromOrigin}})
	testkit.Contains(t, "stderr", e.Run("r", "tool").Stderr, "unknown field: origin")
}

func TestPromote_EmptyAsIsAnError(t *testing.T) {
	e := testkit.New(t)
	proj := toolProject(e, true)
	res := e.RunWith(in(proj), "promote", "tool", "--as", "", "-y")
	if res.Code != 1 || res.Stderr != "error: --as can't be empty\n" {
		t.Errorf("exit %d stderr %q", res.Code, res.Stderr)
	}
	noGlobalRegistry(t, e)
}
