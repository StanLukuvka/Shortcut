package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestStores_Show(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{
		"inline": map[string]any{"description": "Inline item", "inline": "echo hello"},
		"stored": map[string]any{"description": "Stored item", "script": "scripts/stored.sh"},
	})
	e.WriteFile(filepath.Join(e.Store, "scripts", "stored.sh"), "#!/usr/bin/env bash\necho test\n", 0)

	res := e.Run("show", "inline")
	res.WantCode(t, 0)
	testkit.Contains(t, "show inline", res.Stdout, "Name:        inline")
	testkit.Contains(t, "show inline", res.Stdout, "echo hello")
	res = e.Run("show", "stored")
	res.WantCode(t, 0)
	testkit.Contains(t, "show stored", res.Stdout, "Name:        stored")
	testkit.Contains(t, "show stored", res.Stdout, "echo test")
}

func TestEntries_CommandsThatTakeANameShowHowMany(t *testing.T) {
	e := testkit.New(t)
	for _, cmd := range []string{"show", "rm"} {
		testkit.Contains(t, cmd+" --help", e.Run(cmd, "--help").Stdout, "namespace and name")
	}
}

const toolRegistry = `{"version": 1, "shortcuts": {
 "tool": {"description": "A tool: é", "script": "scripts/tool.sh", "confirm": true },
 "bad": {"inline": "x", "cwd": 5, "description": "bd"},
 "gone": {"script": "scripts/none.sh"},
 "trav": {"script": "../../etc/passwd"},
 "nul": null,
 "build": {"description": "Global build", "inline": "echo gb", "cwd": "/tmp"}}}`

// The exact text of show for these (TMP is the test's temp dir).
func TestShow_FullOutput(t *testing.T) {
	e := testkit.New(t)
	e.WriteRegistryRaw(e.Store, toolRegistry)
	e.WriteFile(filepath.Join(e.Store, "scripts", "tool.sh"), "#!/bin/sh\r\necho hi\r\n\n\n   \n", 0)
	cases := []struct {
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{[]string{"show", "tool"}, 0, `Name:        tool
Scope:       global
Defined in:  TMP/home/.shortcuts/shortcuts.json
CWD:         caller
Run CWD:     TMP/home
Description: A tool: é
Confirm:     yes

Script (TMP/home/.shortcuts/scripts/tool.sh):
#!/bin/sh
echo hi
`, ""},
		{[]string{"show", "--json", "tool"}, 0, `{
  "name": "tool",
  "namespace": null,
  "scope": "global",
  "defined_in": "TMP/home/.shortcuts/shortcuts.json",
  "source": null,
  "hides_global": false,
  "approval": "ok",
  "cwd": null,
  "run_cwd": "TMP/home",
  "script_path": "TMP/home/.shortcuts/scripts/tool.sh",
  "error": null,
  "entry": {
    "description": "A tool: é",
    "script": "scripts/tool.sh",
    "confirm": true
  }
}
`, ""},
		{[]string{"show", "--json", "bad"}, 0, `{
  "name": "bad",
  "namespace": null,
  "scope": "global",
  "defined_in": "TMP/home/.shortcuts/shortcuts.json",
  "source": null,
  "hides_global": false,
  "approval": "ok",
  "cwd": null,
  "run_cwd": null,
  "script_path": null,
  "error": "'bad' is not a valid entry: 'cwd' must be a string",
  "entry": {
    "inline": "x",
    "cwd": 5,
    "description": "bd"
  }
}
`, ""},
		{[]string{"show", "--json", "trav"}, 0, `{
  "name": "trav",
  "namespace": null,
  "scope": "global",
  "defined_in": "TMP/home/.shortcuts/shortcuts.json",
  "source": null,
  "hides_global": false,
  "approval": "ok",
  "cwd": null,
  "run_cwd": "TMP/home",
  "script_path": null,
  "error": "path traversal detected: '../../etc/passwd' resolves to 'TMP/etc/passwd', outside 'TMP/home/.shortcuts'.",
  "entry": {
    "script": "../../etc/passwd"
  }
}
`, ""},
		{[]string{"show", "--json", "nul"}, 0, `{
  "name": "nul",
  "namespace": null,
  "scope": "global",
  "defined_in": "TMP/home/.shortcuts/shortcuts.json",
  "source": null,
  "hides_global": false,
  "approval": "ok",
  "cwd": null,
  "run_cwd": null,
  "script_path": null,
  "error": "'nul' is not a valid entry: expected a dictionary",
  "entry": null
}
`, ""},
		{[]string{"show", "gone"}, 0, `Name:        gone
Scope:       global
Defined in:  TMP/home/.shortcuts/shortcuts.json
CWD:         caller
Run CWD:     TMP/home
Description: 

Script (TMP/home/.shortcuts/scripts/none.sh):
  [file missing: TMP/home/.shortcuts/scripts/none.sh]
`, ""},
		{[]string{"show", "bad"}, 1, "", "error: 'bad' is not a valid entry: 'cwd' must be a string\n"},
		{[]string{"show", "nul"}, 1, "", "error: 'nul' is not a valid entry: expected a dictionary\n"},
		{[]string{"show", "a", "b"}, 1, "", "error: shortcut or namespace 'a' not found. A global namespace is the file TMP/home/.shortcuts/imports/a.shortcut.\n"},
		{[]string{"show", "--json", "build", "x"}, 1, "", "error: too many names: build x\n"},
		{[]string{"show", "--json", "x"}, 1, "", "error: shortcut 'x' not found.\n"},
	}
	for _, c := range cases {
		res := e.Run(c.args...)
		want, wantErr := strings.ReplaceAll(c.stdout, "TMP", e.Tmp), strings.ReplaceAll(c.stderr, "TMP", e.Tmp)
		if res.Code != c.code || res.Stdout != want || res.Stderr != wantErr {
			t.Errorf("%v: exit %d\nstdout %q\nstderr %q\nwant exit %d\nstdout %q\nstderr %q", c.args, res.Code, res.Stdout, res.Stderr, c.code, want, wantErr)
		}
	}
}

// A script that is not UTF-8 is shown with one U+FFFD for each run of bad bytes, line ends as \n, trailing spaces, tabs and
// newlines cut, and what could rewrite the screen made visible.
func TestShow_AScriptThatIsNotUTF8(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"inv": map[string]any{"script": "scripts/inv.sh"}})
	e.WriteFile(filepath.Join(e.Store, "scripts", "inv.sh"), []byte("bad \xff\xfe utf \xe2\x82 end \x1b[31m\r\nx\ry \xe2\x80\xa8 \n\x1f \n"), 0)
	res := e.Run("show", "inv")
	want := "\nScript (" + filepath.Join(e.Store, "scripts", "inv.sh") + "):\nbad \uFFFD utf \uFFFD end ^[[31m\nx\ny \\u2028 \n^_"
	if res.Code != 0 || !strings.Contains(res.Stdout, want+"\n") {
		t.Errorf("exit %d: %q", res.Code, res.Stdout)
	}
}

// The show and --json lines of the global-flag case; the whole case is
// TestStores_GlobalFlagReachesAHiddenShortcut in rm_test.go.
func TestShow_GlobalFlagReachesAHiddenShortcut(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"build": testkit.Inline("echo global"), "tool": map[string]any{"script": "scripts/tool.sh", "description": "d"}})
	e.WriteFile(filepath.Join(e.Store, "scripts", "tool.sh"), "#!/usr/bin/env bash\n", 0)
	proj := e.Project(map[string]any{"build": testkit.Inline("echo project"), "tool": map[string]any{"script": "tool.sh", "description": "d"}},
		testkit.Files(map[string]any{"tool.sh": "#!/usr/bin/env bash\n"}))
	e.Approve(filepath.Join(proj, ".shortcuts"))
	in := testkit.Opts{Cwd: proj}

	testkit.Contains(t, "show build", e.RunWith(in, "show", "build").Stdout, "echo project")
	res := e.RunWith(in, "show", "--global", "build")
	testkit.Contains(t, "show --global", res.Stdout, "Scope:       global\n")
	testkit.Contains(t, "show --global", res.Stdout, "echo global")
	if res = e.RunWith(in, "show", "--global", "--field", "script_path", "tool"); res.Stdout != filepath.Join(e.Store, "scripts", "tool.sh")+"\n" {
		t.Errorf("show --global --field script_path: %q", res.Stdout)
	}

	// The project still has one when the global one is gone: every --global command says so
	e.WriteStore(e.Store, map[string]any{})
	hint := "The project in " + proj + " has its own 'build': leave out --global to use it."
	for _, args := range [][]string{{"show", "--global", "build"}, {"show", "--json", "--global", "build"}} {
		res := e.RunWith(in, args...)
		res.WantCode(t, 1)
		testkit.Contains(t, strings.Join(args, " "), res.Stderr, "shortcut 'build' not found.")
		testkit.Contains(t, strings.Join(args, " "), res.Stderr, hint)
	}
	if strings.Contains(e.RunWith(in, "show", "--global", "nothing").Stderr, "has its own") {
		t.Error("a name the project lacks gets the hint")
	}
	if strings.Contains(e.Run("show", "build").Stderr, "has its own") {
		t.Error("outside a project gets the hint")
	}
}

// TestShow_ReadsPastABrokenProject: show still works in a broken project; run, rm and update refuse it
// (their own tests).
func TestShow_ReadsPastABrokenProject(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"outside": testkit.Inline("echo outside"), "docker": testkit.Inline("echo global")})
	broken := e.BrokenProject()
	in := testkit.Opts{Cwd: broken}
	// Looking is not running: show reads past the file, quietly
	for _, args := range [][]string{{"show", "outside"}, {"show", "--json", "outside"}} {
		res := e.RunWith(in, args...)
		for _, bad := range []string{"corrupt registry", "warning"} {
			if strings.Contains(res.Stderr, bad) {
				t.Errorf("%v: stderr has %q: %s", args, bad, res.Stderr)
			}
		}
	}
	testkit.Contains(t, "show outside", e.RunWith(in, "show", "outside").Stdout, "Scope:       global")
	testkit.Contains(t, "show nothing", e.RunWith(in, "show", "nothing").Stderr, "'nothing' not found")
	testkit.Contains(t, "show docker", e.RunWith(in, "show", "docker").Stdout, "Scope:       global")

	// an unknown top-level field: the file still parses, so show finds its entries; they are not approved
	proj := e.Project(map[string]any{"inside": testkit.Inline("true")}, testkit.Name("p2"), testkit.Extra(map[string]any{"bogus": 1}))
	res := e.RunWith(testkit.Opts{Cwd: proj}, "show", "inside")
	res.WantCode(t, 0)
	testkit.Contains(t, "show inside", res.Stdout, "Scope:       project")
	testkit.Contains(t, "show inside", res.Stdout, "Approval:    not approved (run: shortcut update)")
}

// show --field prints one field bare, reads only, and says which fields exist.
func TestShow_Field(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{
		"line": map[string]any{"description": "A line", "inline": "echo hi", "confirm": true},
		"tool": map[string]any{"description": "A tool", "script": "scripts/tool.sh"},
	})
	e.WriteFile(filepath.Join(e.Store, "scripts", "tool.sh"), "#!/bin/sh\n", 0)
	store := filepath.Join(e.Store, "shortcuts.json")
	list := "name, namespace, scope, defined_in, source, hides_global, approval, cwd, run_cwd, script_path, error, description, inline, script"
	cases := []struct {
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{[]string{"show", "--field", "name", "line"}, 0, "line\n", ""},
		{[]string{"show", "--field=scope", "line"}, 0, "global\n", ""},
		{[]string{"show", "--field", "defined_in", "line"}, 0, store + "\n", ""},
		{[]string{"show", "--field", "script_path", "line"}, 0, "", ""},
		{[]string{"show", "--field", "script_path", "tool"}, 0, filepath.Join(e.Store, "scripts", "tool.sh") + "\n", ""},
		{[]string{"show", "--field", "hides_global", "line"}, 0, "false\n", ""},
		{[]string{"show", "--field", "description", "line"}, 0, "A line\n", ""},
		{[]string{"show", "--field", "inline", "line"}, 0, "echo hi\n", ""},
		{[]string{"show", "--field", "script", "tool"}, 0, "scripts/tool.sh\n", ""},
		{[]string{"show", "--field", "error", "line"}, 0, "", ""},
		{[]string{"show", "--field", "confirm", "line"}, 1, "", "error: unknown field 'confirm' (one of: " + list + ")\n"},
		{[]string{"show", "--field", "name", "--json", "line"}, 1, "", "error: --field and --json can't be used together\n"},
		{[]string{"show", "--field", "name", "nothing"}, 1, "", "error: shortcut 'nothing' not found.\n"},
		{[]string{"show", "--field", "name", "a", "b", "c"}, 1, "", ""},
	}
	for _, c := range cases {
		res := e.Run(c.args...)
		if c.stderr == "" && c.code == 1 { // only the code and a one-line error
			if res.Code != 1 || res.Stdout != "" || strings.Count(res.Stderr, "\n") != 1 {
				t.Errorf("%v: exit %d stdout %q stderr %q", c.args, res.Code, res.Stdout, res.Stderr)
			}
			continue
		}
		if res.Code != c.code || res.Stdout != c.stdout || res.Stderr != c.stderr {
			t.Errorf("%v: exit %d stdout %q stderr %q; want exit %d stdout %q stderr %q", c.args, res.Code, res.Stdout, res.Stderr, c.code, c.stdout, c.stderr)
		}
	}
}

// show --field never approves and writes nothing, even for a project entry that was never approved.
func TestShow_FieldIsReadOnly(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"x": testkit.Inline("echo x")})
	before := treeOf(t, e.Tmp)
	res := e.RunWith(in(proj), "show", "--field", "approval", "x")
	if res.Code != 0 || res.Stdout != "new\n" || res.Stderr != "" {
		t.Errorf("exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	if after := treeOf(t, e.Tmp); !reflect.DeepEqual(after, before) {
		t.Errorf("show --field changed the tree:\n%v\n--- now\n%v", before, after)
	}
}

// A caller's folder that is not valid UTF-8 cannot go into JSON: `show --json` refuses it, as every write does,
// while `show --field` prints a field that is not affected.
func TestShow_JSONRefusesANonUTF8FolderAndFieldDoesNot(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hello": testkit.Inline("true")})
	dir := filepath.Join(e.Tmp, "p\xffroj")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Skipf("this filesystem refuses non-UTF-8 names: %v", err)
	}
	res := e.RunWith(testkit.Opts{Cwd: dir}, "show", "--json", "hello")
	if res.Code == 0 || res.Stdout != "" {
		t.Errorf("show --json: exit %d, stdout %q", res.Code, res.Stdout)
	}
	testkit.Contains(t, "stderr", res.Stderr, "not valid UTF-8")
	res = e.RunWith(testkit.Opts{Cwd: dir}, "show", "--field", "name", "hello")
	res.WantCode(t, 0)
	if res.Stdout != "hello\n" {
		t.Errorf("show --field name: %q", res.Stdout)
	}
}
