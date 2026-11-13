// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-4). Don't hand-edit without checking the plan.

package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// sharedEntries is SHARED of test_project_files.py.
func sharedEntries() map[string]any {
	return map[string]any{
		"keep": map[string]any{"description": "shared keep", "inline": "echo shared keep"},
		"swap": map[string]any{"description": "shared swap", "inline": "echo shared swap", "confirm": true},
		"gone": map[string]any{"description": "shared gone", "inline": "echo shared gone"},
	}
}

func writeLocalFile(t *testing.T, e *testkit.Env, proj string, shortcuts map[string]any) {
	t.Helper()
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), docText(t, map[string]any{"version": 2, "shortcuts": shortcuts}), 0)
}

func localData(t *testing.T, proj string) map[string]any {
	t.Helper()
	return fileShortcuts(t, filepath.Join(proj, ".shortcuts.local"))
}

// overlaid is a project with an approved .shortcuts and a .shortcuts.local written by hand and approved.
func overlaid(t *testing.T, e *testkit.Env) string {
	t.Helper()
	proj := e.Project(sharedEntries(), testkit.Dirs("src"))
	writeLocalFile(t, e, proj, map[string]any{"mine": inline("echo mine"), "swap": inline("echo local swap"), "gone": nil})
	e.Approve(filepath.Join(proj, ".shortcuts"))
	return proj
}

// py: test_project_files.py::test_local_adds_replaces_a_whole_entry_and_hides
func TestProjectFiles_LocalAddsReplacesAWholeEntryAndHides(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	eq(t, "keep", e.RunWith(in(proj), "r", "keep").Stdout, "shared keep\n")
	eq(t, "mine", e.RunWith(in(proj), "r", "mine").Stdout, "mine\n")
	// The whole entry: the shared one's confirm doesn't carry over, so this needs no -y
	eq(t, "swap", e.RunWith(in(proj), "r", "swap").Stdout, "local swap\n")
	res := e.RunWith(in(proj), "r", "gone")
	eq(t, "gone exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "not found")
}

// py: test_project_files.py::test_a_null_lets_a_global_shortcut_of_that_name_through
func TestProjectFiles_ANullLetsAGlobalShortcutOfThatNameThrough(t *testing.T) {
	e := testkit.New(t)
	e.Run("new", "gone", "--", "echo global gone")
	proj := overlaid(t, e)
	eq(t, "gone", e.RunWith(in(proj), "r", "gone").Stdout, "global gone\n")
}

// py: test_project_files.py::test_relative_paths_resolve_from_the_project_root_in_both_files
func TestProjectFiles_RelativePathsResolveFromTheProjectRootInBothFiles(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"shared": map[string]any{"script": "bin/run.sh", "cwd": "."}},
		testkit.Dirs("src"), testkit.Files(map[string]any{"bin/run.sh": "#!/usr/bin/env bash\npwd\n"}), testkit.Mode(0o755))
	writeLocalFile(t, e, proj, map[string]any{"mine": map[string]any{"script": "bin/run.sh", "cwd": "src"}})
	e.Approve(filepath.Join(proj, ".shortcuts"))
	src := filepath.Join(proj, "src")
	eq(t, "shared", e.RunWith(in(src), "r", "shared").Stdout, proj+"\n")
	eq(t, "mine", e.RunWith(in(src), "r", "mine").Stdout, src+"\n")
}

// py: test_project_files.py::test_a_project_can_be_only_a_local_file
func TestProjectFiles_AProjectCanBeOnlyALocalFile(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(nil, testkit.Dirs("src"))
	eq(t, "new", e.RunWith(in(proj), "new", "solo", "--local", "--", "echo solo").Code, 0)
	eq(t, "run", e.RunWith(in(filepath.Join(proj, "src")), "r", "solo").Stdout, "solo\n")
}

// py: test_project_files.py::test_imports_stay_in_the_shared_file
func TestProjectFiles_ImportsStayInTheSharedFile(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{})
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"),
		docText(t, map[string]any{"version": 2, "imports": map[string]any{"x": "x.shortcut"}, "shortcuts": map[string]any{}}), 0)
	contains(t, "list", e.RunWith(in(proj), "list").Stderr, "can't have 'imports'")
}

// py: test_project_files.py::test_new_local_overrides_a_shared_name_and_says_so
func TestProjectFiles_NewLocalOverridesASharedNameAndSaysSo(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	res := e.RunWith(in(proj), "new", "keep", "--local", "--", "echo mine now")
	contains(t, "stderr", res.Stderr, "this overrides 'keep'")
	eq(t, "run", e.RunWith(in(proj), "r", "keep").Stdout, "mine now\n")
}

// py: test_project_files.py::test_new_local_takes_a_command_and_add_records_the_path
func TestProjectFiles_NewLocalTakesACommandAndAddRecordsThePath(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{}, testkit.Files(map[string]any{"tools/run.sh": "#!/usr/bin/env bash\n"}))
	outside := e.Script("outside.sh", "#!/usr/bin/env bash\n")
	res := e.RunWith(in(proj), "new", "x", "--local", "--desc", "d")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "after '--'")
	eq(t, "in", e.RunWith(in(proj), "new", "in", "--script", "tools/run.sh", "--desc", "d", "--local").Code, 0)
	eq(t, "out", e.RunWith(in(proj), "new", "out", "--script", outside, "--desc", "d", "--local").Code, 0)
	eq(t, "in script", localData(t, proj)["in"].(map[string]any)["script"], "tools/run.sh")
	eq(t, "out script", localData(t, proj)["out"].(map[string]any)["script"], outside)
	var names []string
	entries, _ := os.ReadDir(proj)
	for _, en := range entries {
		names = append(names, en.Name())
	}
	slices.Sort(names)
	eq(t, "files", names, []string{".shortcuts", ".shortcuts.local", "tools"})
}

// py: test_project_files.py::test_local_only_works_at_the_project_root
func TestProjectFiles_LocalOnlyWorksAtTheProjectRoot(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	src := filepath.Join(proj, "src")
	res := e.RunWith(in(src), "new", "x", "--local", "--", "true")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "is inside the project at")
	notExist(t, filepath.Join(src, ".shortcuts.local"))
	eq(t, "keep", e.RunWith(in(src), "r", "keep").Stdout, "shared keep\n")
}

// py: test_project_files.py::test_a_new_local_file_is_excluded_from_git_once
func TestProjectFiles_ANewLocalFileIsExcludedFromGitOnce(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{})
	e.RunCmd(testkit.Opts{}, "git", "init", "-q", proj)
	e.RunWith(in(proj), "new", "a", "--local", "--", "true")
	e.RunWith(in(proj), "new", "b", "--local", "--", "true")
	exclude := readFile(t, filepath.Join(proj, ".git", "info", "exclude"))
	n := 0
	for _, line := range strings.Split(exclude, "\n") {
		if line == ".shortcuts.local" {
			n++
		}
	}
	eq(t, "exclude lines", n, 1)
	eq(t, "status", e.RunCmd(testkit.Opts{Cwd: proj}, "git", "status", "--porcelain").Stdout, "?? .shortcuts\n")
}

// py: test_project_files.py::test_outside_git_nothing_is_excluded
func TestProjectFiles_OutsideGitNothingIsExcluded(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{})
	eq(t, "new", e.RunWith(in(proj), "new", "a", "--local", "--", "true").Code, 0)
	notExist(t, filepath.Join(proj, ".git"))
}

// py: test_project_files.py::test_show_json_says_which_file_an_entry_comes_from
func TestProjectFiles_ShowJSONSaysWhichFileAnEntryComesFrom(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	info := decodeObject(t, e.RunWith(in(proj), "show", "--json", "mine").Stdout)
	eq(t, "entry", info["entry"], map[string]any{"inline": "echo mine"})
	eq(t, "name", info["name"], "mine")
	eq(t, "namespace", info["namespace"], nil)
	eq(t, "scope", info["scope"], "project")
	eq(t, "source", info["source"], "local")
	eq(t, "defined_in", info["defined_in"], filepath.Join(proj, ".shortcuts.local"))
	eq(t, "script_path", info["script_path"], nil)
}

// py: test_project_files.py::test_a_hand_edit_to_local_needs_approving
func TestProjectFiles_AHandEditToLocalNeedsApproving(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	writeLocalFile(t, e, proj, map[string]any{"mine": inline("echo evil")})
	res := e.RunWith(in(proj), "r", "keep")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "changed since you approved it")
	res = e.RunWith(in(proj), "update", "-y")
	contains(t, "diff", res.Stdout, "+++ b/.shortcuts.local")
	contains(t, "diff", res.Stdout, "echo evil")
	eq(t, "mine", e.RunWith(in(proj), "r", "mine").Stdout, "evil\n")
}

// py: test_project_files.py::test_a_local_file_from_a_clone_needs_approving
func TestProjectFiles_ALocalFileFromACloneNeedsApproving(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"keep": inline("echo keep")})
	writeLocalFile(t, e, proj, map[string]any{"evil": inline("echo evil")})
	res := e.RunWith(in(proj), "r", "evil")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "isn't approved yet")
}

// py: test_project_files.py::test_a_cli_write_does_not_approve_a_hand_edit_to_local
func TestProjectFiles_ACLIWriteDoesNotApproveAHandEditToLocal(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	writeLocalFile(t, e, proj, map[string]any{"mine": inline("echo evil")})
	res := e.RunWith(in(proj), "new", "more", "--local", "--", "echo more")
	contains(t, "stderr", res.Stderr, "needs approving")
	eq(t, "mine", e.RunWith(in(proj), "r", "mine").Code, 1)
	eq(t, "more", e.RunWith(in(proj), "r", "more").Code, 1)
}

// py: test_project_files.py::test_a_cli_write_does_not_approve_a_change_to_shared
func TestProjectFiles_ACLIWriteDoesNotApproveAChangeToShared(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	changed := sharedEntries()
	changed["keep"] = inline("echo evil")
	e.WriteStore(filepath.Join(proj, ".shortcuts"), changed)
	res := e.RunWith(in(proj), "new", "more", "--local", "--", "echo more")
	contains(t, "stderr", res.Stderr, "needs approving")
	eq(t, "keep", e.RunWith(in(proj), "r", "keep").Code, 1)
	eq(t, "more", e.RunWith(in(proj), "r", "more").Code, 1)
}

// py: test_project_files.py::test_a_cli_write_to_a_fresh_clone_does_not_approve_shared
func TestProjectFiles_ACLIWriteToAFreshCloneDoesNotApproveShared(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"keep": inline("echo keep")})
	res := e.RunWith(in(proj), "new", "more", "--local", "--", "echo more")
	contains(t, "stderr", res.Stderr, "needs approving")
	res = e.RunWith(in(proj), "r", "keep")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "isn't approved yet")
	res = e.RunWith(in(proj), "update", "-y")
	contains(t, "diff", res.Stdout, "+++ b/.shortcuts\n")
	eq(t, "keep", e.RunWith(in(proj), "r", "keep").Stdout, "keep\n")
}

// py: test_project_files.py::test_a_script_named_in_local_is_covered
func TestProjectFiles_AScriptNamedInLocalIsCovered(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{}, testkit.Files(map[string]any{"run.sh": "#!/usr/bin/env bash\necho one\n"}), testkit.Mode(0o755))
	eq(t, "new", e.RunWith(in(proj), "new", "go", "--script", "run.sh", "--desc", "d", "--local").Code, 0)
	e.Approve(filepath.Join(proj, ".shortcuts"))
	eq(t, "run", e.RunWith(in(proj), "r", "go").Stdout, "one\n")
	e.WriteFile(filepath.Join(proj, "run.sh"), "#!/usr/bin/env bash\necho two\n", 0)
	eq(t, "changed", e.RunWith(in(proj), "r", "go").Code, 1)
}

// py: test_project_files.py::test_a_diff_marks_what_local_overrides_or_hides
func TestProjectFiles_ADiffMarksWhatLocalOverridesOrHides(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(sharedEntries())
	writeLocalFile(t, e, proj, map[string]any{"swap": inline("echo local swap"), "gone": nil})
	res := e.RunWith(in(proj), "update", "-y")
	contains(t, "diff", res.Stdout, "'swap' in .shortcuts is overridden by .shortcuts.local for you.")
	contains(t, "diff", res.Stdout, "'gone' in .shortcuts is hidden by .shortcuts.local for you.")
}

// py: test_project_files.py::test_list_shows_each_entrys_source
func TestProjectFiles_ListShowsEachEntrysSource(t *testing.T) {
	e := testkit.New(t)
	out := e.RunWith(in(overlaid(t, e)), "list").Stdout
	matches(t, out, `keep +caller +shared `)
	matches(t, out, `mine +caller +local `)
	matches(t, out, `swap +caller +overridden `)
	matches(t, out, `gone +caller +hidden `)
}

// py: test_project_files.py::test_a_hidden_name_is_not_offered
func TestProjectFiles_AHiddenNameIsNotOffered(t *testing.T) {
	e := testkit.New(t)
	names := strings.Fields(e.RunWith(in(overlaid(t, e)), "list", "--names").Stdout)
	if !slices.Contains(names, "keep") || slices.Contains(names, "gone") {
		t.Errorf("names: %v", names)
	}
}

// py: test_project_files.py::test_show_names_the_file_that_defines_it
func TestProjectFiles_ShowNamesTheFileThatDefinesIt(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	res := e.RunWith(in(proj), "show", "swap")
	contains(t, "show", res.Stdout, "Defined in:  "+filepath.Join(proj, ".shortcuts.local"))
	contains(t, "show", res.Stdout, "Source:      overridden")
}

// py: test_project_files.py::test_the_cli_never_writes_the_shared_file
func TestProjectFiles_TheCliNeverWritesTheSharedFile(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	before := readFile(t, filepath.Join(proj, ".shortcuts"))
	source := e.Script("tool.sh", "#!/usr/bin/env bash\n")
	for _, args := range [][]string{
		{"new", "a", "--local", "--", "echo a"},
		{"new", "b", "--script", source, "--desc", "d", "--local"},
		{"rm", "--local", "keep", "-y"},
		{"rm", "--local", "swap", "-y"},
	} {
		eq(t, strings.Join(args, " "), e.RunWith(in(proj), args...).Code, 0)
	}
	eq(t, "shared", readFile(t, filepath.Join(proj, ".shortcuts")), before)
}

// py: test_project_files.py::test_a_write_without_local_is_refused_for_a_project_entry
func TestProjectFiles_AWriteWithoutLocalIsRefusedForAProjectEntry(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	for _, args := range [][]string{{"rm", "keep", "-y"}, {"rm", "mine", "-y"}} {
		res := e.RunWith(in(proj), args...)
		eq(t, strings.Join(args, " "), res.Code, 1)
		contains(t, strings.Join(args, " "), res.Stderr, "shortcut doesn't change .shortcuts")
	}
}

// py: test_project_files.py::test_rm_local_hides_a_shared_entry_and_undoes_itself
func TestProjectFiles_RmLocalHidesASharedEntryAndUndoesItself(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	contains(t, "hide", e.RunWith(in(proj), "rm", "--local", "keep", "-y").Stdout, "Hid shortcut")
	if v, has := localData(t, proj)["keep"]; !has || v != nil {
		t.Errorf("keep = %v, want null", v)
	}
	eq(t, "hidden", e.RunWith(in(proj), "r", "keep").Code, 1)
	contains(t, "undo", e.RunWith(in(proj), "rm", "--local", "keep", "-y").Stdout, "Removed 'keep'")
	eq(t, "back", e.RunWith(in(proj), "r", "keep").Stdout, "shared keep\n")
}

// py: test_project_files.py::test_rm_local_of_an_override_brings_the_shared_entry_back
func TestProjectFiles_RmLocalOfAnOverrideBringsTheSharedEntryBack(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	eq(t, "rm", e.RunWith(in(proj), "rm", "--local", "swap", "-y").Code, 0)
	eq(t, "run", e.RunWith(in(proj), "r", "-y", "swap").Stdout, "shared swap\n")
}

// py: test_project_files.py::test_rm_local_says_the_shared_entry_applies_again
func TestProjectFiles_RmLocalSaysTheSharedEntryAppliesAgain(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	rc, out := e.InTerminal(in(proj), []testkit.Key{{Wait: "[y/N]", Send: y("n\n")}}, "rm", "--local", "swap")
	eq(t, "exit", rc, 1)
	contains(t, "removes", out, "Removes your 'swap' from "+filepath.Join(proj, ".shortcuts.local")+".")
	contains(t, "again", out, "The 'swap' in .shortcuts applies again.")
	_, out = e.InTerminal(in(proj), []testkit.Key{{Wait: "[y/N]", Send: y("n\n")}}, "rm", "--local", "gone")
	contains(t, "gone", out, "Stops hiding 'gone'")
}

// py: test_project_files.py::test_a_cli_write_keeps_the_approval
func TestProjectFiles_ACliWriteKeepsTheApproval(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	res := e.RunWith(in(proj), "new", "more", "--local", "--", "echo more")
	if strings.Contains(res.Stderr, "needs approving") {
		t.Errorf("new: %q", res.Stderr)
	}
	eq(t, "more", e.RunWith(in(proj), "r", "more").Stdout, "more\n")
	eq(t, "keep", e.RunWith(in(proj), "r", "keep").Stdout, "shared keep\n")
	eq(t, "rm", e.RunWith(in(proj), "rm", "--local", "more", "-y").Code, 0)
	eq(t, "keep again", e.RunWith(in(proj), "r", "keep").Stdout, "shared keep\n")
}
