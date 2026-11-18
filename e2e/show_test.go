package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestStores_BrokenProjectRefusesANamespaceItImported(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"docker": testkit.Inline("echo global")})
	proj := e.Project(map[string]any{},
		testkit.Extra(map[string]any{"imports": map[string]any{"docker": "../tools/docker.shortcut"}}),
		testkit.Files(map[string]string{"tools/docker.shortcut": `{"version": 1, "shortcuts": {"ps": {"inline": "true"}}}`}))
	e.CorruptStore(filepath.Join(proj, ".shortcuts"))

	res := e.RunWith(in(proj), "r", ".", "docker", "ps")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "Nothing runs inside this project until it's fixed.")
	// The file can't be read, so its imports are gone too: show finds the global 'docker'
	testkit.Contains(t, "show", e.RunWith(in(proj), "show", "docker").Stdout, "Scope:       global")
}

func TestStores_AnUnknownTopLevelFieldStopsTheProjectToo(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"outside": testkit.Inline("echo outside")})
	proj := e.Project(map[string]any{"inside": testkit.Inline("true"), "mine": testkit.Inline("true")})
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 1, "shortcuts": {"mine": {"inline": "echo mine"}}}`, 0)
	registry := filepath.Join(proj, ".shortcuts")
	e.WriteStore(registry, map[string]any{"inside": testkit.Inline("true"), "mine": testkit.Inline("true")}, map[string]any{"bogus": 1})

	for _, name := range []string{"inside", "mine"} {
		res := e.RunWith(in(proj), "r", ".", name)
		res.WantCode(t, 1)
		testkit.Contains(t, name, res.Stderr, registry+" has an unknown field: bogus. Nothing runs inside this project")
	}

	// The file still parses, so show finds its entries
	res := e.RunWith(in(proj), "show", ".", "inside")
	res.WantCode(t, 0)
	testkit.Contains(t, "show", res.Stdout, "Scope:       project")
	// show and rm --local find its entries
	testkit.Eq(t, "rm inside", e.RunWith(in(proj), "rm", "inside", "-y").Code, 1) // rm never reaches a project entry
	res = e.RunWith(in(proj), "rm", "--local", "mine", "-y")
	res.WantCode(t, 0)
	if _, has := localData(t, proj)["mine"]; has {
		t.Error("mine is still in .shortcuts.local")
	}
	testkit.Contains(t, "registry", testkit.ReadFile(t, registry), `"bogus":1`)
}

func TestProjectFiles_ShowJSONSaysWhichFileAnEntryComesFrom(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	info := decodeObject(t, e.RunWith(in(proj), "show", "--json", ".", "mine").Stdout)
	testkit.Eq(t, "entry", info["entry"], map[string]any{"inline": "echo mine"})
	testkit.Eq(t, "name", info["name"], "mine")
	testkit.Eq(t, "namespace", info["namespace"], nil)
	testkit.Eq(t, "scope", info["scope"], "project")
	testkit.Eq(t, "source", info["source"], "local")
	testkit.Eq(t, "defined_in", info["defined_in"], filepath.Join(proj, ".shortcuts.local"))
	testkit.Eq(t, "script_path", info["script_path"], nil)
	// an override: show names the file that defines it, and says so
	res := e.RunWith(in(proj), "show", ".", "swap")
	testkit.Contains(t, "show", res.Stdout, "Defined in:  "+filepath.Join(proj, ".shortcuts.local"))
	testkit.Contains(t, "show", res.Stdout, "Source:      overridden")
}

func TestRunning_ShowJSONIsOneObjectWithWhereItResolves(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"s": map[string]any{"script": "scripts/s.sh", "cwd": "/tmp"}, "c": testkit.Inline("true")})
	e.Folder("home/.shortcuts/scripts")
	info := decodeObject(t, e.Run("show", "--json", "s").Stdout)
	for k, want := range map[string]any{"name": "s", "namespace": nil, "scope": "global", "defined_in": filepath.Join(e.Store, "shortcuts.json"),
		"source": nil, "script_path": filepath.Join(e.Store, "scripts", "s.sh"), "cwd": "/tmp", "run_cwd": "/tmp",
		"error": nil, "entry": map[string]any{"script": "scripts/s.sh", "cwd": "/tmp"}} {
		testkit.Eq(t, k, info[k], want)
	}
	info = decodeObject(t, e.RunWith(in(e.Tmp), "show", "--json", "c").Stdout)
	testkit.Eq(t, "cwd", info["cwd"], nil)
	testkit.Eq(t, "run_cwd", info["run_cwd"], e.Tmp)
}

func TestRunning_ShowJSONNamesANamespaceEntryBare(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "dk", testkit.NS(map[string]any{"ps": map[string]any{"inline": "true", "cwd": "."}}))
	info := decodeObject(t, e.Run("show", "--json", "dk", "ps").Stdout)
	for k, want := range map[string]any{"name": "ps", "namespace": "dk", "scope": "namespace",
		"defined_in": filepath.Join(e.Store, "imports", "dk.shortcut"), "cwd": ".", "run_cwd": filepath.Join(e.Store, "imports")} {
		testkit.Eq(t, k, info[k], want)
	}
}

func TestRunning_ShowDirTakesZeroOrOne(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hi": testkit.Inline("echo hi")})
	for v, want := range map[string]string{"0": "", "1": "→ "} {
		res := e.RunWith(testkit.Opts{Env: map[string]string{"SHORTCUT_SHOW_DIR": v}}, "r", "hi")
		if res.Code != 0 || res.Stdout != "hi\n" || (want == "") != (res.Stderr == "") || !strings.HasPrefix(res.Stderr, want) {
			t.Errorf("%q: %+v", v, res)
		}
	}
	for _, v := range []string{"", "2", "yes"} {
		e.RunWith(testkit.Opts{Env: map[string]string{"SHORTCUT_SHOW_DIR": v}}, "r", "hi").Want(t, 1, "", "error: SHORTCUT_SHOW_DIR must be 0 or 1, not '"+v+"'\n")
	}
}

func TestEntries_CommandsThatTakeANameShowHowMany(t *testing.T) {
	e := testkit.New(t)
	for _, cmd := range []string{"show", "rm"} {
		testkit.Contains(t, cmd+" --help", e.Run(cmd, "--help").Stdout, "namespace and name")
	}
}

// The exact text of show for these (TMP is the test's temp dir).
func TestShow_FullOutput(t *testing.T) {
	e := testkit.New(t)
	e.WriteRegistryRaw(e.Store, `{"version": 1, "shortcuts": {
 "tool": {"description": "A tool: é", "script": "scripts/tool.sh", "confirm": true },
 "bad": {"inline": "x", "cwd": 5, "description": "bd"},
 "gone": {"script": "scripts/none.sh"},
 "gonecwd": {"inline": "true", "cwd": "/no/such/folder"},
 "trav": {"script": "../../etc/passwd"},
 "nul": null,
 "build": {"description": "Global build", "inline": "echo gb", "cwd": "/tmp"}}}`)
	e.Folder("etc") // the folder of "trav"'s script, outside the store, must be there for the traversal check
	e.WriteFile(filepath.Join(e.Store, "scripts", "tool.sh"), "#!/bin/sh\necho hi\n\n\n   \n", 0)
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
		{[]string{"show", "build"}, 0, `Name:        build
Scope:       global
Defined in:  TMP/home/.shortcuts/shortcuts.json
CWD:         /tmp
Run CWD:     /tmp
Description: Global build

Inline command:
  echo gb
`, ""},
		{[]string{"show", "gonecwd"}, 0, `Name:        gonecwd
Scope:       global
Defined in:  TMP/home/.shortcuts/shortcuts.json
CWD:         /no/such/folder
Run CWD:     /no/such/folder
Description: 

Inline command:
  true
`, ""},
		{[]string{"show", "gone"}, 1, `Name:        gone
Scope:       global
Defined in:  TMP/home/.shortcuts/shortcuts.json
CWD:         caller
Run CWD:     TMP/home
Description: 

Script (TMP/home/.shortcuts/scripts/none.sh):
`, "error: open TMP/home/.shortcuts/scripts/none.sh: no such file or directory\n"},
		{[]string{"show", "bad"}, 1, "", "error: 'bad' is not a valid entry: 'cwd' must be a string\n"},
		{[]string{"show", "nul"}, 1, "", "error: 'nul' is not a valid entry: expected a JSON object\n"},
		{[]string{"show", "a", "b"}, 1, "", "error: shortcut or namespace 'a' not found.\n"},
		{[]string{"show", "--json", "build", "x"}, 1, "", "error: too many names: build x\n"},
		{[]string{"show", "--json", "x"}, 1, "", "error: shortcut 'x' not found.\n"},
	}
	for _, c := range cases {
		res := e.Run(c.args...)
		want, wantErr := strings.ReplaceAll(c.stdout, "TMP", e.Tmp), strings.ReplaceAll(c.stderr, "TMP", e.Tmp)
		res.Want(t, c.code, want, wantErr)
	}
}

// TestShow_ReadsPastABrokenProject: show still works in a broken project; run and rm refuse it
// (their own tests).
func TestShow_ReadsPastABrokenProject(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"outside": testkit.Inline("echo outside"), "docker": testkit.Inline("echo global")})
	broken := e.BrokenProject()
	o := testkit.Opts{Cwd: broken}
	// Looking is not running: show reads past the file, quietly
	for _, args := range [][]string{{"show", "outside"}, {"show", "--json", "outside"}} {
		res := e.RunWith(o, args...)
		for _, bad := range []string{"corrupt registry", "warning"} {
			if strings.Contains(res.Stderr, bad) {
				t.Errorf("%v: stderr has %q: %s", args, bad, res.Stderr)
			}
		}
	}
	testkit.Contains(t, "show outside", e.RunWith(o, "show", "outside").Stdout, "Scope:       global")
	testkit.Contains(t, "show nothing", e.RunWith(o, "show", "nothing").Stderr, "'nothing' not found")
	testkit.Contains(t, "show docker", e.RunWith(o, "show", "docker").Stdout, "Scope:       global")
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
		{[]string{"show", "--field", "confirm", "line"}, 1, "", "error: unknown field 'confirm'\n"},
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
		res.Want(t, c.code, c.stdout, c.stderr)
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
