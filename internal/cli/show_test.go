// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-2). Don't hand-edit without checking the plan.

package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// py: test_stores.py::test_show_and_location
func TestStores_ShowAndLocation(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{
		"inline": map[string]any{"description": "Inline item", "inline": "echo hello"},
		"stored": map[string]any{"description": "Stored item", "script": "scripts/stored.sh"},
	})
	e.WriteFile(filepath.Join(e.Store, "scripts", "stored.sh"), "#!/usr/bin/env bash\necho test\n", 0)

	res := e.Run("show", "inline")
	if res.Code != 0 {
		t.Errorf("show inline: exit %d", res.Code)
	}
	contains(t, "show inline", res.Stdout, "Name:        inline")
	contains(t, "show inline", res.Stdout, "echo hello")
	res = e.Run("show", "stored")
	if res.Code != 0 {
		t.Errorf("show stored: exit %d", res.Code)
	}
	contains(t, "show stored", res.Stdout, "Name:        stored")
	contains(t, "show stored", res.Stdout, "echo test")

	res = e.Run("location", "stored")
	if res.Code != 0 || !strings.HasSuffix(strings.TrimSpace(res.Stdout), "scripts/stored.sh") {
		t.Errorf("location stored: exit %d %q", res.Code, res.Stdout)
	}
}

// py: test_stores.py::test_location_prints_a_bare_path_or_fails
func TestStores_LocationPrintsABarePathOrFails(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"inline_one": inline("echo hi"), "scripted": map[string]any{"script": "scripts/scripted.sh"}})
	e.WriteFile(filepath.Join(e.Store, "scripts", "scripted.sh"), "#!/usr/bin/env bash\n", 0)
	res := e.Run("location", "inline_one")
	if res.Code != 1 || res.Stdout != "" {
		t.Errorf("inline: exit %d stdout %q", res.Code, res.Stdout)
	}
	contains(t, "inline", res.Stderr, "inline shortcut")
	if res = e.Run("location", "scripted"); res.Stdout != filepath.Join(e.Store, "scripts", "scripted.sh")+"\n" {
		t.Errorf("scripted: %q", res.Stdout)
	}
}

// py: test_stores.py::test_commands_that_take_a_name_show_how_many
func TestEntries_CommandsThatTakeANameShowHowMany(t *testing.T) {
	e := testkit.New(t)
	for _, cmd := range []string{"show", "location", "rm"} {
		contains(t, cmd+" --help", e.Run(cmd, "--help").Stdout, "[namespace] name\n")
	}
}

const toolRegistry = `{"version": 2, "shortcuts": {
 "tool": {"description": "A tool: é", "script": "scripts/tool.sh", "args": [{"name":"a","required":true,"desc":"first\nline"},{"name":"b","default":"x y"},{"name":"r","rest":true}], "confirm": true, "created": "2026-01-02", "created_by": "me", "icon": "wrench"},
 "bad": {"inline": "x", "cwd": 5, "description": "bd"},
 "gone": {"script": "scripts/none.sh"},
 "trav": {"script": "../../etc/passwd"},
 "nul": null,
 "build": {"description": "Global build", "inline": "echo gb", "cwd": "/tmp"}}}`

// What the Python version printed for these, byte for byte (TMP is the test's temp dir).
func TestShow_MatchesPython(t *testing.T) {
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
Usage:       shortcut r tool <a> [b] [r...]
Arguments:
  a     required        first\nline
  b     default: 'x y'
  r...  optional
Confirm:     True
Created:     2026-01-02
Created by:  me
Icon:        wrench

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
    "description": "A tool: \u00e9",
    "script": "scripts/tool.sh",
    "args": [
      {
        "name": "a",
        "required": true,
        "desc": "first\nline"
      },
      {
        "name": "b",
        "default": "x y"
      },
      {
        "name": "r",
        "rest": true
      }
    ],
    "confirm": true,
    "created": "2026-01-02",
    "created_by": "me",
    "icon": "wrench"
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
		{[]string{"location", "tool"}, 0, "TMP/home/.shortcuts/scripts/tool.sh\n", ""},
		{[]string{"location", "build"}, 1, "", "error: 'build' is an inline shortcut, so it has no file. Change it in the file `shortcut show` names.\n"},
		{[]string{"location", "trav"}, 1, "", "error: path traversal detected: '../../etc/passwd' resolves to 'TMP/etc/passwd', outside 'TMP/home/.shortcuts'.\n"},
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

// A script that is not UTF-8 is shown with U+FFFD for each bad byte or cut sequence, line ends as \n, trailing white space (U+2028 and
// U+001F too) cut, and what could rewrite the screen made visible (Python: errors="replace", universal newlines, rstrip, safe).
func TestShow_AScriptThatIsNotUTF8(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"inv": map[string]any{"script": "scripts/inv.sh"}})
	e.WriteFile(filepath.Join(e.Store, "scripts", "inv.sh"), []byte("bad \xff\xfe utf \xe2\x82 end \x1b[31m\r\nx\ry \xe2\x80\xa8 \n\x1f \n"), 0)
	res := e.Run("show", "inv")
	want := "\nScript (" + filepath.Join(e.Store, "scripts", "inv.sh") + "):\nbad \uFFFD\uFFFD utf \uFFFD end ^[[31m\nx\ny"
	if res.Code != 0 || !strings.Contains(res.Stdout, want+"\n") {
		t.Errorf("exit %d: %q", res.Code, res.Stdout)
	}
}

// py: the show, location and --json lines of test_global_flag_reaches_a_hidden_shortcut; its rm and run lines are
// P-5's and P-3's, so that test stays unclaimed until then.
func TestShow_GlobalFlagReachesAHiddenShortcut(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"build": inline("echo global"), "tool": map[string]any{"script": "scripts/tool.sh", "description": "d"}})
	e.WriteFile(filepath.Join(e.Store, "scripts", "tool.sh"), "#!/usr/bin/env bash\n", 0)
	proj := e.Project(map[string]any{"build": inline("echo project"), "tool": map[string]any{"script": "tool.sh", "description": "d"}},
		testkit.Files(map[string]any{"tool.sh": "#!/usr/bin/env bash\n"}))
	e.Approve(filepath.Join(proj, ".shortcuts"))
	in := testkit.Opts{Cwd: proj}

	contains(t, "show build", e.RunWith(in, "show", "build").Stdout, "echo project")
	res := e.RunWith(in, "show", "--global", "build")
	contains(t, "show --global", res.Stdout, "Scope:       global\n")
	contains(t, "show --global", res.Stdout, "echo global")
	if res = e.RunWith(in, "location", "--global", "tool"); res.Stdout != filepath.Join(e.Store, "scripts", "tool.sh")+"\n" {
		t.Errorf("location --global: %q", res.Stdout)
	}

	// The project still has one when the global one is gone: every --global command says so
	e.WriteStore(e.Store, map[string]any{})
	hint := "The project in " + proj + " has its own 'build': leave out --global to use it."
	for _, args := range [][]string{{"show", "--global", "build"}, {"location", "--global", "build"}, {"show", "--json", "--global", "build"}} {
		res := e.RunWith(in, args...)
		if res.Code != 1 {
			t.Errorf("%v: exit %d", args, res.Code)
		}
		contains(t, strings.Join(args, " "), res.Stderr, "shortcut 'build' not found.")
		contains(t, strings.Join(args, " "), res.Stderr, hint)
	}
	if strings.Contains(e.RunWith(in, "show", "--global", "nothing").Stderr, "has its own") {
		t.Error("a name the project lacks gets the hint")
	}
	if strings.Contains(e.Run("show", "build").Stderr, "has its own") {
		t.Error("outside a project gets the hint")
	}
}

// py: the show and location lines of test_broken_project_stops_every_run_inside_it and
// test_broken_project_refuses_a_namespace_it_imported; their run, rm and update lines are P-3's and P-5's.
func TestShow_ReadsPastABrokenProject(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"outside": inline("echo outside"), "docker": inline("echo global")})
	broken := e.BrokenProject()
	in := testkit.Opts{Cwd: broken}
	// Looking is not running: show and location read past the file, quietly
	for _, args := range [][]string{{"show", "outside"}, {"show", "--json", "outside"}, {"location", "outside"}} {
		res := e.RunWith(in, args...)
		for _, bad := range []string{"corrupt registry", "warning"} {
			if strings.Contains(res.Stderr, bad) {
				t.Errorf("%v: stderr has %q: %s", args, bad, res.Stderr)
			}
		}
	}
	contains(t, "show outside", e.RunWith(in, "show", "outside").Stdout, "Scope:       global")
	contains(t, "show nothing", e.RunWith(in, "show", "nothing").Stderr, "'nothing' not found")
	contains(t, "show docker", e.RunWith(in, "show", "docker").Stdout, "Scope:       global")

	// an unknown top-level field: the file still parses, so show finds its entries; they are not approved
	proj := e.Project(map[string]any{"inside": inline("true")}, testkit.Name("p2"), testkit.Extra(map[string]any{"bogus": 1}))
	res := e.RunWith(testkit.Opts{Cwd: proj}, "show", "inside")
	if res.Code != 0 {
		t.Errorf("show inside: exit %d: %s", res.Code, res.Stderr)
	}
	contains(t, "show inside", res.Stdout, "Scope:       project")
	contains(t, "show inside", res.Stdout, "Approval:    not approved (run: shortcut update)")
}
