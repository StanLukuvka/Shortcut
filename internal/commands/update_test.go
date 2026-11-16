package commands

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func tree(t *testing.T, roots ...string) []string {
	t.Helper()
	var paths []string
	for _, root := range roots {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			paths = append(paths, p)
			return nil
		})
	}
	slices.Sort(paths)
	return paths
}

func TestApproval_BlocksUntilApprovedAndAfterAnyChange(t *testing.T) {
	e := testkit.New(t)
	shortcuts := map[string]any{
		"hi":    map[string]any{"description": "Say hi", "script": "scripts/hi.sh"},
		"there": map[string]any{"description": "There", "inline": "echo there"},
	}
	proj := e.Project(shortcuts, testkit.Files(map[string]any{"scripts/hi.sh": "#!/usr/bin/env bash\necho hi\n"}))
	script, registry := filepath.Join(proj, "scripts", "hi.sh"), filepath.Join(proj, ".shortcuts")
	o := in(proj)

	// First run blocked; full listing on first approval
	res := e.RunWith(o, "r", "hi")
	res.WantCode(t, 1)
	testkit.Contains(t, "first run", res.Stderr, "isn't approved yet")
	res = e.RunWith(o, "update")
	res.WantCode(t, 1)
	testkit.Contains(t, "update", res.Stderr, "in a terminal, or pass -y")
	for _, want := range []string{"  new      .shortcuts\n", "  new      scripts/hi.sh\n"} {
		testkit.Contains(t, "update", res.Stdout, want)
	}
	if code := e.RunWith(o, "r", "hi").Code; code != 1 {
		t.Errorf("run after a refused update: exit %d", code)
	}

	runOK(t, e, o, "update", "-y")
	if out := e.RunWith(o, "r", "hi").Stdout; out != "hi\n" {
		t.Errorf("approved run: %q", out)
	}
	testkit.Contains(t, "update .", e.RunWith(o, "update", ".").Stdout, "already approved")
	testkit.Contains(t, "update", e.RunWith(o, "update").Stdout, "Nothing to approve here")

	// Reordered keys and reformatting change nothing
	e.WriteRegistryRaw(registry, "{\n       \"shortcuts\": {\n       \"there\": {\"description\": \"There\", \"inline\": \"echo there\"},\n"+
		"       \"hi\": {\"description\": \"Say hi\", \"script\": \"scripts/hi.sh\"}},\n       \"version\": 1}")
	if code := e.RunWith(o, "r", "hi").Code; code != 0 {
		t.Errorf("reordered: exit %d", code)
	}

	// A change to the JSON blocks, also the shortcuts that didn't change
	shortcuts["there"] = map[string]any{"description": "There", "inline": "echo changed"}
	e.WriteStore(registry, shortcuts)
	res = e.RunWith(o, "r", "hi")
	res.WantCode(t, 1)
	testkit.Contains(t, "changed json", res.Stderr, proj+" changed since you approved it; run: shortcut update")
	out := runOK(t, e, o, "update", "-y").Stdout
	testkit.Contains(t, "list", out, "  changed  .shortcuts\n")
	if strings.Count(out, "\n  ") != 1 {
		t.Errorf("only .shortcuts changed:\n%s", out)
	}

	// So does a change to a script
	e.WriteFile(script, "#!/usr/bin/env bash\necho evil\n", 0)
	if code := e.RunWith(o, "r", "hi").Code; code != 1 {
		t.Errorf("changed script: exit %d", code)
	}
	out = runOK(t, e, o, "update", "-y").Stdout
	testkit.Contains(t, "list", out, "  changed  scripts/hi.sh\n")
	if got := e.RunWith(o, "r", "hi").Stdout; got != "evil\n" {
		t.Errorf("run after approving the change: %q", got)
	}

	// The script going away is a change, and can't be approved while the entry needs it
	os.Remove(script)
	if code := e.RunWith(o, "r", "there").Code; code != 1 {
		t.Errorf("script gone: exit %d", code)
	}
	res = e.RunWith(o, "update", ".", "-y")
	res.WantCode(t, 1)
	testkit.Contains(t, "update", res.Stderr, "script not found")
}

func TestApproval_IsKeptOutsideWhatItApproves(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(testkit.HI)
	before := tree(t, proj)
	e.Approve(filepath.Join(proj, ".shortcuts"))
	if after := tree(t, proj); !slices.Equal(before, after) {
		t.Errorf("approving wrote in the project: %v -> %v", before, after)
	}
	records, _ := filepath.Glob(filepath.Join(e.Store, "approvals", "*.json"))
	if len(records) != 1 {
		t.Fatalf("records: %v", records)
	}
	raw, _ := os.ReadFile(records[0])
	var record struct {
		Key   string
		Files map[string]string
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record.Key != proj {
		t.Errorf("key %q, want %q", record.Key, proj)
	}
	if _, ok := record.Files[".shortcuts"]; !ok {
		t.Errorf("record lacks .shortcuts: %v", record.Files)
	}
}

func TestApproval_OneChangedGroupLeavesTheOthersRunning(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(testkit.OWN)
	docker := e.WriteImport(filepath.Join(proj, ".shortcuts"), "docker", testkit.DockerPS)
	e.WriteImport(filepath.Join(proj, ".shortcuts"), "kube", testkit.KubeGet)
	e.Approve(filepath.Join(proj, ".shortcuts"))

	e.WriteFile(docker, `{"version": 1, "shortcuts": {"ps": {"inline": "echo evil"}}}`, 0)
	res := e.RunWith(in(proj), "r", "docker", "ps")
	res.WantCode(t, 1)
	testkit.Contains(t, "docker", res.Stderr, "docker changed since you approved it; run: shortcut update docker")
	if out := e.RunWith(in(proj), "r", "kube", "get").Stdout; out != "get\n" {
		t.Errorf("kube: %q", out)
	}
	if out := e.RunWith(in(proj), "r", "own").Stdout; out != "own\n" {
		t.Errorf("own: %q", out)
	}
	runOK(t, e, in(proj), "update", "docker", "-y")
	if out := e.RunWith(in(proj), "r", "docker", "ps").Stdout; out != "evil\n" {
		t.Errorf("docker after update: %q", out)
	}
}

func TestApproval_SwappingTwoApprovedNamespacesNeedsApproval(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(testkit.OWN)
	reg := filepath.Join(proj, ".shortcuts")
	e.WriteImport(reg, "dev", testkit.NS(map[string]any{"deploy": testkit.Inline("echo dev")}))
	e.WriteImport(reg, "prod", testkit.NS(map[string]any{"deploy": testkit.Inline("echo prod")}))
	e.Approve(reg)
	if out := e.RunWith(in(proj), "r", "dev", "deploy").Stdout; out != "dev\n" {
		t.Errorf("before: %q", out)
	}

	e.WriteStore(reg, testkit.OWN, map[string]any{"imports": map[string]any{"dev": "imports/prod.shortcut", "prod": "imports/dev.shortcut"}})
	res := e.RunWith(in(proj), "r", "dev", "deploy")
	if res.Code != 1 || strings.Contains(res.Stdout, "prod") {
		t.Errorf("swapped: exit %d stdout %q", res.Code, res.Stdout)
	}
	testkit.Contains(t, "swapped", res.Stderr, "dev isn't approved yet; review it with: shortcut update dev")

	runOK(t, e, in(proj), "update", "dev", "-y")
	if out := e.RunWith(in(proj), "r", "dev", "deploy").Stdout; out != "prod\n" {
		t.Errorf("after: %q", out)
	}
}

func TestApproval_ANamespaceApprovedInOneProjectIsNewInAnother(t *testing.T) {
	e := testkit.New(t)
	first := e.Project(testkit.OWN, testkit.Name("first"))
	tools := e.WriteImport(filepath.Join(first, ".shortcuts"), "tools", testkit.NS(map[string]any{"go": testkit.Inline("echo go")}))
	e.Approve(filepath.Join(first, ".shortcuts"))
	second := e.Project(map[string]any{}, testkit.Name("second"), testkit.Extra(map[string]any{"imports": map[string]any{"tools": tools}}))
	res := e.RunWith(in(second), "r", "tools", "go")
	res.WantCode(t, 1)
	testkit.Contains(t, "second", res.Stderr, "tools isn't approved yet")
	if out := e.RunWith(in(first), "r", "tools", "go").Stdout; out != "go\n" {
		t.Errorf("first: %q", out)
	}
	testkit.Contains(t, "update", e.RunWith(in(second), "update", "-y").Stdout, "New to you: tools ("+tools+")")
}

func TestApproval_CoversAProjectScriptLinkedFromOutside(t *testing.T) {
	e := testkit.New(t)
	outside := e.WriteFile(filepath.Join(e.Tmp, "outside.sh"), "echo one\n", 0)
	proj := e.Project(map[string]any{"go": map[string]any{"script": outside}}, testkit.Approved())
	e.WriteFile(outside, "echo two\n", 0)
	res := e.RunWith(in(proj), "r", "go")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "changed since you approved it")
}

func TestApproval_ANamespaceApprovalCoversItsWholeScriptsFolder(t *testing.T) {
	e := testkit.New(t)
	folder := filepath.Join(e.Store, "imports", "scripts", "dk")
	e.WriteImport(e.Store, "dk", testkit.NS(map[string]any{"ps": map[string]any{"script": "scripts/dk/ps.sh"}}))
	e.WriteFile(filepath.Join(folder, "ps.sh"), "#!/usr/bin/env bash\n. \"$(dirname \"$0\")/lib/say\"\nsay ps\n", 0o755)
	say := e.WriteFile(filepath.Join(folder, "lib", "say"), "say() { echo \"$1\"; }\n", 0)
	runOK(t, e, testkit.Opts{}, "update", "-y", "dk")
	if out := e.Run("r", "dk", "ps").Stdout; out != "ps\n" {
		t.Errorf("approved run: %q", out)
	}

	changed := func(what string) {
		t.Helper()
		res := e.Run("r", "dk", "ps")
		if res.Code != 1 || !strings.Contains(res.Stderr, "dk changed since you approved it") {
			t.Errorf("%s: exit %d stderr %q", what, res.Code, res.Stderr)
		}
		runOK(t, e, testkit.Opts{}, "update", "-y", "dk")
		if code := e.Run("r", "dk", "ps").Code; code != 0 {
			t.Errorf("%s: run after update: exit %d", what, code)
		}
	}
	e.WriteFile(say, "say() { echo \"changed $1\"; }\n", 0)
	changed("a changed helper")
	e.WriteFile(filepath.Join(folder, "new-helper"), "anything\n", 0)
	changed("a new file")
	e.Symlink(e.Folder("elsewhere"), filepath.Join(folder, "more"))
	changed("a new symlinked folder")
	os.Remove(filepath.Join(folder, "more"))
	e.Symlink(e.Folder("other"), filepath.Join(folder, "more"))
	changed("a symlink moved")
	// A folder that isn't the namespace's is not
	e.WriteFile(filepath.Join(e.Store, "imports", "scripts", "other", "x"), "x\n", 0)
	if code := e.Run("r", "dk", "ps").Code; code != 0 {
		t.Errorf("another namespace's folder: exit %d", code)
	}
}

func TestApproval_ApprovingSaysWhatItCovers(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(testkit.GO)
	testkit.Contains(t, "update", e.RunWith(in(proj), "update", "-y").Stdout, "not what they call")
}

func TestApproval_ACwdWithANulDoesNotStopTheToolsThatFindIt(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(testkit.GO, testkit.Approved())
	e.WriteStore(filepath.Join(proj, ".shortcuts"), map[string]any{"go": map[string]any{"inline": "echo ran", "cwd": "a\x00b"}})
	for _, args := range [][]string{{"list"}, {"update", "-y"}} {
		res := e.RunWith(in(proj), args...)
		for _, bad := range []string{"embedded null", "Traceback", "panic", "goroutine"} {
			if strings.Contains(res.Stderr, bad) {
				t.Errorf("%v: stderr has %q: %s", args, bad, res.Stderr)
			}
		}
	}
}

func TestApproval_NonUTF8Script(t *testing.T) {
	e := testkit.New(t)
	tool := "bin/tool"
	proj := e.Project(testkit.BinTool, testkit.Files(map[string]any{tool: []byte("#!/bin/sh\n# \xff\xfe\necho one\n")}), testkit.Approved())
	if out := e.RunWith(in(proj), "r", "tool").Stdout; out != "one\n" {
		t.Errorf("first run: %q", out)
	}
	e.WriteFile(filepath.Join(proj, tool), []byte("#!/bin/sh\n# \xff\xfd\necho two\n"), 0)
	if code := e.RunWith(in(proj), "r", "tool").Code; code != 1 {
		t.Errorf("changed: exit %d", code)
	}
	testkit.Contains(t, "list", e.RunWith(in(proj), "update", "-y").Stdout, "  changed  bin/tool\n")
	if code := e.RunWith(in(proj), "r", "tool").Code; code != 0 {
		t.Errorf("after update: exit %d", code)
	}
}

func TestApproval_ANulCannotHideAScriptBashWouldRun(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(testkit.BinTool, testkit.Files(map[string]any{"bin/tool": []byte("#!/bin/bash\necho PWNED\n\x00")}))
	testkit.Contains(t, "list", e.RunWith(in(proj), "update", "-y").Stdout, "  new      bin/tool\n")
}

// What `update` shows for a namespace: the file it is read from.
func TestUpdate_ShowsAGroupDescriptionAndItsFile(t *testing.T) {
	e := testkit.New(t)
	path := e.WriteImport(e.Store, "docker", map[string]any{"version": 1, "description": "Docker housekeeping",
		"shortcuts": map[string]any{"ps": testkit.Inline("echo ps")}})
	out := runOK(t, e, testkit.Opts{}, "update", "docker", "-y").Stdout
	testkit.Contains(t, "update docker", out, "New to you: docker ("+path+")\n")
	testkit.Contains(t, "update docker", out, "Approved docker. This covers its definitions and its scripts folder, not what they call.")
}

// update's own arguments and refusals.
func TestUpdate_Refusals(t *testing.T) {
	e := testkit.New(t)
	res := e.Run("update", ".")
	if res.Code != 1 || res.Stderr != "error: no project shortcuts here: no .shortcuts or .shortcuts.local in this folder or its parents\n" {
		t.Errorf("update .: exit %d stderr %q", res.Code, res.Stderr)
	}
	res = e.Run("update", "nothing")
	if res.Code != 1 || res.Stderr != "error: no namespace named 'nothing' here\n" {
		t.Errorf("update nothing: exit %d stderr %q", res.Code, res.Stderr)
	}
	if res = e.Run("update"); res.Code != 0 || res.Stdout != "Nothing to approve here.\n" {
		t.Errorf("update: exit %d stdout %q", res.Code, res.Stdout)
	}
	// an entry that can't run is not approved, and says so for each; unnamed, that is a warning and exit 0
	proj := e.Project(map[string]any{"a": map[string]any{"script": "none.sh"}, "b": map[string]any{"inline": "true", "cwd": 1}})
	res = e.RunWith(in(proj), "update", "-y")
	if res.Code != 0 || strings.Count(res.Stderr, "warning: skipped "+proj+": ") != 2 || strings.Contains(res.Stderr, "error:") {
		t.Errorf("update -y: exit %d stderr %q", res.Code, res.Stderr)
	}
	// named with ".", it is an error
	res = e.RunWith(in(proj), "update", ".", "-y")
	if res.Code != 1 || strings.Count(res.Stderr, "error: cannot approve "+proj+": ") != 2 {
		t.Errorf("update . -y: exit %d stderr %q", res.Code, res.Stderr)
	}
	// the local-over-shared line
	proj = e.Project(map[string]any{"x": map[string]any{"inline": "true", "cwd": "sub"}, "y": testkit.Inline("true")}, testkit.Name("p2"))
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 1, "shortcuts": {"y": {"inline": "false"}}}`, 0)
	res = e.RunWith(in(proj), "update", "-y")
	testkit.Contains(t, "local", res.Stdout, "'y' in .shortcuts is overridden by .shortcuts.local for you.\n")
}

// Q-3: a file name that is not valid UTF-8 cannot be approved, and the refusal shows it escaped, never as a raw 0x9b.
func TestUpdate_NonUTF8PathIsRefusedAndShownEscaped(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{"ps": testkit.Inline("echo ps")}))
	bad := filepath.Join(e.Store, "imports", "scripts", "docker", "a\x9bb\xed\xa0\x80c.sh")
	if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("echo hi\n"), 0o644); err != nil {
		t.Skipf("this filesystem refuses non-UTF-8 names: %v", err)
	}
	res := e.Run("update", "docker", "-y")
	res.WantCode(t, 1)
	testkit.Contains(t, "update", res.Stderr, `"scripts/docker/a\x9bb\xed\xa0\x80c.sh" is not valid UTF-8; rename it`)
	if !utf8.ValidString(res.Stdout+res.Stderr) || strings.ContainsAny(res.Stdout+res.Stderr, "\u0080\u009b") {
		t.Errorf("output holds raw bytes: %q %q", res.Stdout, res.Stderr)
	}
}

func TestStores_ShortcutFileNeedsTheCurrentVersion(t *testing.T) {
	e := testkit.New(t)
	path := e.WriteImport(e.Store, "docker", map[string]any{"shortcuts": map[string]any{"ps": testkit.Inline("echo ps")}})
	res := e.Run("update", "docker", "-y")
	res.WantCode(t, 1)
	testkit.Contains(t, "no version", res.Stderr, `needs "version": 1`)

	e.WriteFile(path, `{"version": 3, "shortcuts": {"ps": {"inline": "echo ps"}}}`, 0)
	res = e.Run("update", "docker", "-y")
	res.WantCode(t, 1)
	testkit.Contains(t, "version 3", res.Stderr, "newer than this shortcut reads; upgrade shortcut")
}

func TestStores_AFileWrittenAsVersion2IsNewerThanThisFormat(t *testing.T) {
	e := testkit.New(t)
	e.WriteRegistryRaw(e.Store, `{"version": 2, "shortcuts": {"v": {"inline": "echo ran"}}}`)
	res := e.Run("list") // list warns about a store it cannot read and goes on
	testkit.Contains(t, "list", res.Stderr, "is format version 2, newer than this shortcut reads; upgrade shortcut")
	testkit.Contains(t, "list names the file", res.Stderr, "shortcuts.json")
}

func TestStores_AVersionThatIsntAWholeNumberIsRefused(t *testing.T) {
	e := testkit.New(t)
	for _, version := range []string{`"3"`, "3.0", "null", "true"} {
		e.WriteRegistryRaw(e.Store, `{"version": `+version+`, "shortcuts": {"v": {"inline": "echo ran"}}}`)
		res := e.Run("r", "v")
		if res.Code != 1 || res.Stdout != "" {
			t.Errorf("%s: exit %d stdout %q", version, res.Code, res.Stdout)
		}
		testkit.Contains(t, version, res.Stderr, "'version' must be a whole number, such as 1")
	}
	proj := e.Project(testkit.GO, testkit.Extra(map[string]any{"version": "3"}))
	res := e.RunWith(in(proj), "update", ".", "-y")
	res.WantCode(t, 1)
	testkit.Contains(t, "project", res.Stderr, "'version' must be a whole number")
}

// C9: `update -y` approves the good namespace and skips an unrelated broken one; naming the broken one fails.
func TestUpdate_UnnamedSkipsABrokenNamespace(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "good", testkit.NS(map[string]any{"ps": testkit.Inline("echo ps")}))
	e.WriteImport(e.Store, "bad", testkit.NS(map[string]any{"a": map[string]any{"script": "none.sh"}}))
	res := e.Run("update", "-y")
	if res.Code != 0 || !strings.Contains(res.Stdout, "Approved good.") || !strings.Contains(res.Stderr, "warning: skipped bad: ") ||
		strings.Contains(res.Stderr, "error:") {
		t.Errorf("update -y: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	res = e.Run("update", "bad", "-y")
	if res.Code != 1 || !strings.Contains(res.Stderr, "error: cannot approve bad: ") {
		t.Errorf("update bad -y: exit %d stderr %q", res.Code, res.Stderr)
	}
}

func TestUpdate_ListsNewChangedAndRemovedPaths(t *testing.T) {
	e := testkit.New(t)
	script := func(name string) any { return map[string]any{"script": name} }
	proj := e.Project(map[string]any{"a": script("a.sh"), "b": script("b.sh"), "c": script("c.sh")}, testkit.Approved(),
		testkit.Files(map[string]any{"a.sh": "echo a\n", "b.sh": "echo b\n", "c.sh": "echo c\n"}))
	o := in(proj)
	e.WriteFile(filepath.Join(proj, "b.sh"), "echo B\n", 0)
	e.WriteFile(filepath.Join(proj, "d.sh"), "echo d\n", 0)
	if err := os.Remove(filepath.Join(proj, "c.sh")); err != nil {
		t.Fatal(err)
	}
	e.WriteStore(filepath.Join(proj, ".shortcuts"), map[string]any{"a": script("a.sh"), "b": script("b.sh"), "d": script("d.sh")})
	out := runOK(t, e, o, "update", "-y").Stdout
	testkit.Contains(t, "list", out, "Changes to "+proj+"\n  changed  .shortcuts\n  changed  b.sh\n  removed  c.sh\n  new      d.sh\nApproved")
}
