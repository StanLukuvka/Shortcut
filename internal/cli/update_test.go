// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-3). Don't hand-edit without checking the plan.

package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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

// py: test_approval.py::test_approval_blocks_until_approved_and_after_any_change
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
	if res.Code != 1 {
		t.Errorf("first run: exit %d", res.Code)
	}
	contains(t, "first run", res.Stderr, "isn't approved yet")
	res = e.RunWith(o, "update")
	if res.Code != 1 {
		t.Errorf("update with no terminal: exit %d", res.Code)
	}
	contains(t, "update", res.Stderr, "in a terminal, or pass -y")
	for _, want := range []string{"+++ b/.shortcuts\n", "+++ b/scripts/hi.sh", "+echo hi"} {
		contains(t, "update", res.Stdout, want)
	}
	if code := e.RunWith(o, "r", "hi").Code; code != 1 {
		t.Errorf("run after a refused update: exit %d", code)
	}

	runOK(t, e, o, "update", "-y")
	if out := e.RunWith(o, "r", "hi").Stdout; out != "hi\n" {
		t.Errorf("approved run: %q", out)
	}
	contains(t, "update .", e.RunWith(o, "update", ".").Stdout, "already approved")
	contains(t, "update", e.RunWith(o, "update").Stdout, "Nothing to approve here")

	// Reordered keys and reformatting change nothing
	e.WriteRegistryRaw(registry, "{\n       \"shortcuts\": {\n       \"there\": {\"description\": \"There\", \"inline\": \"echo there\"},\n"+
		"       \"hi\": {\"description\": \"Say hi\", \"script\": \"scripts/hi.sh\"}},\n       \"version\": 2}")
	if code := e.RunWith(o, "r", "hi").Code; code != 0 {
		t.Errorf("reordered: exit %d", code)
	}

	// A change to the JSON blocks, also the shortcuts that didn't change
	shortcuts["there"] = map[string]any{"description": "There", "inline": "echo changed"}
	e.WriteStore(registry, shortcuts)
	res = e.RunWith(o, "r", "hi")
	if res.Code != 1 {
		t.Errorf("changed json: exit %d", res.Code)
	}
	contains(t, "changed json", res.Stderr, proj+" changed since you approved it; run: shortcut update")
	out := runOK(t, e, o, "update", "-y").Stdout
	contains(t, "diff", strings.ReplaceAll(out, "-      \"inline\": \"echo there\"", "-    \"there\": {"), "-    \"there\": {")
	contains(t, "diff", out, "+      \"inline\": \"echo changed\"")
	if strings.Contains(out, "+    \"version\"") {
		t.Errorf("version shows as changed:\n%s", out)
	}

	// So does a change to a script
	e.WriteFile(script, "#!/usr/bin/env bash\necho evil\n", 0)
	if code := e.RunWith(o, "r", "hi").Code; code != 1 {
		t.Errorf("changed script: exit %d", code)
	}
	out = runOK(t, e, o, "update", "-y").Stdout
	for _, want := range []string{"--- a/scripts/hi.sh", "-echo hi", "+echo evil"} {
		contains(t, "diff", out, want)
	}
	if got := e.RunWith(o, "r", "hi").Stdout; got != "evil\n" {
		t.Errorf("run after approving the change: %q", got)
	}

	// The script going away is a change, and can't be approved while the entry needs it
	os.Remove(script)
	if code := e.RunWith(o, "r", "there").Code; code != 1 {
		t.Errorf("script gone: exit %d", code)
	}
	res = e.RunWith(o, "update", "-y")
	if res.Code != 1 {
		t.Errorf("update with the script gone: exit %d", res.Code)
	}
	contains(t, "update", res.Stderr, "script not found")
}

// py: test_approval.py::test_approval_is_kept_outside_what_it_approves
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
		Key      string
		Snapshot map[string]string
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record.Key != proj {
		t.Errorf("key %q, want %q", record.Key, proj)
	}
	if _, ok := record.Snapshot[".shortcuts"]; !ok {
		t.Errorf("snapshot lacks .shortcuts: %v", record.Snapshot)
	}
}

// py: test_approval.py::test_one_changed_group_leaves_the_others_running
func TestApproval_OneChangedGroupLeavesTheOthersRunning(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(testkit.OWN)
	docker := e.WriteImport(filepath.Join(proj, ".shortcuts"), "docker", testkit.DockerPS)
	e.WriteImport(filepath.Join(proj, ".shortcuts"), "kube", testkit.KubeGet)
	e.Approve(filepath.Join(proj, ".shortcuts"))

	e.WriteFile(docker, `{"version": 2, "shortcuts": {"ps": {"inline": "echo evil"}}}`, 0)
	res := e.RunWith(in(proj), "r", "docker", "ps")
	if res.Code != 1 {
		t.Errorf("exit %d", res.Code)
	}
	contains(t, "docker", res.Stderr, "docker changed since you approved it; run: shortcut update docker")
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

// py: test_approval.py::test_swapping_two_approved_namespaces_needs_approval
func TestApproval_SwappingTwoApprovedNamespacesNeedsApproval(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(testkit.OWN)
	reg := filepath.Join(proj, ".shortcuts")
	e.WriteImport(reg, "dev", map[string]any{"version": 2, "shortcuts": map[string]any{"deploy": inline("echo dev")}})
	e.WriteImport(reg, "prod", map[string]any{"version": 2, "shortcuts": map[string]any{"deploy": inline("echo prod")}})
	e.Approve(reg)
	if out := e.RunWith(in(proj), "r", "dev", "deploy").Stdout; out != "dev\n" {
		t.Errorf("before: %q", out)
	}

	e.WriteStore(reg, testkit.OWN, map[string]any{"imports": map[string]any{"dev": "imports/prod.shortcut", "prod": "imports/dev.shortcut"}})
	res := e.RunWith(in(proj), "r", "dev", "deploy")
	if res.Code != 1 || strings.Contains(res.Stdout, "prod") {
		t.Errorf("swapped: exit %d stdout %q", res.Code, res.Stdout)
	}
	contains(t, "swapped", res.Stderr, "dev isn't approved yet; review it with: shortcut update dev")

	runOK(t, e, in(proj), "update", "dev", "-y")
	if out := e.RunWith(in(proj), "r", "dev", "deploy").Stdout; out != "prod\n" {
		t.Errorf("after: %q", out)
	}
}

// py: test_approval.py::test_a_namespace_approved_in_one_project_is_new_in_another
func TestApproval_ANamespaceApprovedInOneProjectIsNewInAnother(t *testing.T) {
	e := testkit.New(t)
	first := e.Project(testkit.OWN, testkit.Name("first"))
	tools := e.WriteImport(filepath.Join(first, ".shortcuts"), "tools", map[string]any{"version": 2, "shortcuts": map[string]any{"go": inline("echo go")}})
	e.Approve(filepath.Join(first, ".shortcuts"))
	second := e.Project(map[string]any{}, testkit.Name("second"), testkit.Extra(map[string]any{"imports": map[string]any{"tools": tools}}))
	res := e.RunWith(in(second), "r", "tools", "go")
	if res.Code != 1 {
		t.Errorf("exit %d", res.Code)
	}
	contains(t, "second", res.Stderr, "tools isn't approved yet")
	if out := e.RunWith(in(first), "r", "tools", "go").Stdout; out != "go\n" {
		t.Errorf("first: %q", out)
	}
	contains(t, "update", e.RunWith(in(second), "update", "-y").Stdout, "New to you: tools ("+tools+")")
}

// py: test_approval.py::test_approval_covers_a_project_script_linked_from_outside
func TestApproval_CoversAProjectScriptLinkedFromOutside(t *testing.T) {
	e := testkit.New(t)
	outside := e.WriteFile(filepath.Join(e.Tmp, "outside.sh"), "echo one\n", 0)
	proj := e.Project(map[string]any{"go": map[string]any{"script": outside}}, testkit.Approved())
	e.WriteFile(outside, "echo two\n", 0)
	res := e.RunWith(in(proj), "r", "go")
	if res.Code != 1 {
		t.Errorf("exit %d", res.Code)
	}
	contains(t, "stderr", res.Stderr, "changed since you approved it")
}

// py: test_approval.py::test_a_namespace_approval_covers_its_whole_scripts_folder
func TestApproval_ANamespaceApprovalCoversItsWholeScriptsFolder(t *testing.T) {
	e := testkit.New(t)
	folder := filepath.Join(e.Store, "imports", "scripts", "dk")
	e.WriteImport(e.Store, "dk", map[string]any{"version": 2, "shortcuts": map[string]any{"ps": map[string]any{"script": "scripts/dk/ps.sh"}}})
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

// py: test_approval.py::test_approving_says_what_it_covers
func TestApproval_ApprovingSaysWhatItCovers(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(testkit.GO)
	contains(t, "update", e.RunWith(in(proj), "update", "-y").Stdout, "not what they call")
}

// py: test_approval.py::test_a_cwd_with_a_nul_does_not_stop_the_tools_that_find_it
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

// py: test_approval.py::test_non_utf8_script
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
	out := e.RunWith(in(proj), "update", "-y").Stdout
	if strings.Contains(out, "Binary file") {
		t.Errorf("a text file shown as binary:\n%s", out)
	}
	contains(t, "diff", out, "+echo two")
	contains(t, "diff", out, `+# \xff\xfd`)
}

// py: test_approval.py::test_a_nul_cannot_hide_a_script_bash_would_run
func TestApproval_ANulCannotHideAScriptBashWouldRun(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(testkit.BinTool, testkit.Files(map[string]any{"bin/tool": []byte("#!/bin/bash\necho PWNED\n\x00")}))
	out := e.RunWith(in(proj), "update", "-y").Stdout
	contains(t, "diff", out, "+echo PWNED")
	if strings.Contains(out, "Binary file") {
		t.Errorf("shown as binary:\n%s", out)
	}
}

// What `update` shows besides the diff: a namespace's description is part of the file, and so of what is reviewed
// (the line P-2 left out of test_group_description_shows_once_approved), and a reviewed namespace names the file.
func TestUpdate_ShowsAGroupDescriptionAndItsFile(t *testing.T) {
	e := testkit.New(t)
	path := e.WriteImport(e.Store, "docker", map[string]any{"version": 2, "description": "Docker housekeeping",
		"shortcuts": map[string]any{"ps": inline("echo ps")}})
	out := runOK(t, e, testkit.Opts{}, "update", "docker", "-y").Stdout
	contains(t, "update docker", out, "Docker housekeeping")
	contains(t, "update docker", out, "New to you: docker ("+path+")\n")
	contains(t, "update docker", out, "Approved docker. This covers its definitions and its scripts folder, not what they call.")
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
	// an entry that can't run is not approved, and says so for each
	proj := e.Project(map[string]any{"a": map[string]any{"script": "none.sh"}, "b": map[string]any{"inline": "true", "cwd": 1}})
	res = e.RunWith(in(proj), "update", "-y")
	if res.Code != 1 || strings.Count(res.Stderr, "error: cannot approve "+proj+": ") != 2 {
		t.Errorf("update -y: exit %d stderr %q", res.Code, res.Stderr)
	}
	// the exported-but-relative-cwd warning and the local-over-shared line
	proj = e.Project(map[string]any{"x": map[string]any{"inline": "true", "cwd": "sub", "export": true}, "y": inline("true")}, testkit.Name("p2"))
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 2, "shortcuts": {"y": {"inline": "false"}}}`, 0)
	res = e.RunWith(in(proj), "update", "-y")
	contains(t, "warning", res.Stderr, "warning: 'x' is exported, but its cwd 'sub' only exists in this checkout\n")
	contains(t, "local", res.Stdout, "'y' in .shortcuts is overridden by .shortcuts.local for you.\n")
}
