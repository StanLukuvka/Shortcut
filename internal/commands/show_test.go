package commands

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
	e.Folder("etc") // the folder of "trav"'s script, outside the store, must be there for the traversal check
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
  "cwd": null,
  "defined_in": "TMP/home/.shortcuts/shortcuts.json",
  "entry": {
    "confirm": true,
    "description": "A tool: é",
    "script": "scripts/tool.sh"
  },
  "error": null,
  "name": "tool",
  "namespace": null,
  "run_cwd": "TMP/home",
  "scope": "global",
  "script_path": "TMP/home/.shortcuts/scripts/tool.sh",
  "source": null
}
`, ""},
		{[]string{"show", "--json", "bad"}, 0, `{
  "cwd": null,
  "defined_in": "TMP/home/.shortcuts/shortcuts.json",
  "entry": {
    "cwd": 5,
    "description": "bd",
    "inline": "x"
  },
  "error": "'bad' is not a valid entry: 'cwd' must be a string",
  "name": "bad",
  "namespace": null,
  "run_cwd": null,
  "scope": "global",
  "script_path": null,
  "source": null
}
`, ""},
		{[]string{"show", "--json", "trav"}, 0, `{
  "cwd": null,
  "defined_in": "TMP/home/.shortcuts/shortcuts.json",
  "entry": {
    "script": "../../etc/passwd"
  },
  "error": "path traversal detected: '../../etc/passwd' resolves to 'TMP/etc/passwd', outside 'TMP/home/.shortcuts'.",
  "name": "trav",
  "namespace": null,
  "run_cwd": "TMP/home",
  "scope": "global",
  "script_path": null,
  "source": null
}
`, ""},
		{[]string{"show", "--json", "nul"}, 0, `{
  "cwd": null,
  "defined_in": "TMP/home/.shortcuts/shortcuts.json",
  "entry": null,
  "error": "'nul' is not a valid entry: expected a JSON object",
  "name": "nul",
  "namespace": null,
  "run_cwd": null,
  "scope": "global",
  "script_path": null,
  "source": null
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
		{[]string{"show", "nul"}, 1, "", "error: 'nul' is not a valid entry: expected a JSON object\n"},
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

// show reaches the global store without the dot and only the project with it; each says where the other has the name.
func TestShow_TheDotSelectsTheScope(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"build": testkit.Inline("echo global"), "gonly": testkit.Inline("echo gonly"),
		"tool": map[string]any{"script": "scripts/tool.sh", "description": "d"}})
	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{"ps": testkit.Inline("echo global-ps")}))
	e.WriteFile(filepath.Join(e.Store, "scripts", "tool.sh"), "#!/usr/bin/env bash\n", 0)
	proj := e.Project(map[string]any{"build": testkit.Inline("echo project"), "ponly": testkit.Inline("echo ponly"),
		"tool": map[string]any{"script": "tool.sh", "description": "d"}},
		testkit.Files(map[string]any{"tool.sh": "#!/usr/bin/env bash\n"}))
	e.WriteImport(filepath.Join(proj, ".shortcuts"), "terraform", testkit.NS(map[string]any{"plan": testkit.Inline("echo plan")}))
	in := testkit.Opts{Cwd: proj}

	res := e.RunWith(in, "show", "build")
	testkit.Contains(t, "show build", res.Stdout, "Scope:       global\n")
	testkit.Contains(t, "show build", res.Stdout, "echo global")
	res = e.RunWith(in, "show", ".", "build")
	testkit.Contains(t, "show . build", res.Stdout, "Scope:       project\n")
	testkit.Contains(t, "show . build", res.Stdout, "echo project")
	testkit.Eq(t, "global script_path", e.RunWith(in, "show", "--field", "script_path", "tool").Stdout, filepath.Join(e.Store, "scripts", "tool.sh")+"\n")
	testkit.Eq(t, "project json scope", decodeObject(t, e.RunWith(in, "show", "--json", ".", "build").Stdout)["scope"], "project")
	testkit.Eq(t, "project field", e.RunWith(in, "show", "--field", "scope", ".", "build").Stdout, "project\n")
	testkit.Contains(t, "show . terraform plan", e.RunWith(in, "show", ".", "terraform", "plan").Stdout, "echo plan")
	testkit.Contains(t, "show docker ps", e.RunWith(in, "show", "docker", "ps").Stdout, "echo global-ps")

	// Not reached from the other side: an error that names the command, nothing else
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"show", "ponly"}, "shortcut 'ponly' not found. The project in " + proj + " has it: shortcut show . ponly"},
		{[]string{"show", "--json", "ponly"}, "shortcut show . ponly"},
		{[]string{"show", "terraform", "plan"}, "The project in " + proj + " has it: shortcut show . terraform plan"},
		{[]string{"show", ".", "gonly"}, "shortcut 'gonly' not found. Your global store has it: shortcut show gonly"},
		{[]string{"show", ".", "docker", "ps"}, "Your global store has it: shortcut show docker ps"},
	} {
		res := e.RunWith(in, c.args...)
		res.WantCode(t, 1)
		testkit.Eq(t, strings.Join(c.args, " ")+" stdout", res.Stdout, "")
		testkit.Contains(t, strings.Join(c.args, " "), res.Stderr, c.want)
	}
	// Outside every project
	res = e.Run("show", ".", "build")
	res.WantCode(t, 1)
	testkit.Contains(t, "outside", res.Stderr, "error: no project here")
	e.RunWith(in, "show", ".").WantCode(t, 2)
}

// TestShow_ReadsPastABrokenProject: show still works in a broken project; run and rm refuse it
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

	// an unknown top-level field: the file still parses, so show finds its entries
	proj := e.Project(map[string]any{"inside": testkit.Inline("true")}, testkit.Name("p2"), testkit.Extra(map[string]any{"bogus": 1}))
	res := e.RunWith(testkit.Opts{Cwd: proj}, "show", ".", "inside")
	res.WantCode(t, 0)
	testkit.Contains(t, "show inside", res.Stdout, "Scope:       project")
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
	list := "name, namespace, scope, defined_in, source, cwd, run_cwd, script_path, error, description, inline, script"
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

// show --field only reads: every file in the store and the project is the same afterwards, and none is added or removed.
func TestShow_FieldWritesNothing(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"g": map[string]any{"description": "G", "script": "scripts/g.sh"}})
	e.WriteFile(filepath.Join(e.Store, "scripts", "g.sh"), "#!/bin/sh\n", 0o755)
	proj := e.Project(testkit.GO)
	store, project := treeOf(t, e.Store), treeOf(t, proj)
	for _, args := range [][]string{{"show", "--field", "description", "g"}, {"show", "--field", "inline", ".", "go"}} {
		runOK(t, e, in(proj), args...)
	}
	if got := treeOf(t, e.Store); !reflect.DeepEqual(got, store) {
		t.Errorf("the store changed: %v -> %v", store, got)
	}
	if got := treeOf(t, proj); !reflect.DeepEqual(got, project) {
		t.Errorf("the project changed: %v -> %v", project, got)
	}
}

// `show --field` prints a field that is not affected by a caller's folder that is not valid UTF-8.
func TestShow_FieldWorksInANonUTF8Folder(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hello": testkit.Inline("true")})
	dir := filepath.Join(e.Tmp, "p\xffroj")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Skipf("this filesystem refuses non-UTF-8 names: %v", err)
	}
	res := e.RunWith(testkit.Opts{Cwd: dir}, "show", "--field", "name", "hello")
	res.WantCode(t, 0)
	if res.Stdout != "hello\n" {
		t.Errorf("show --field name: %q", res.Stdout)
	}
}
