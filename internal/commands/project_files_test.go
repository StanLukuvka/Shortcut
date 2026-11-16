package commands

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// sharedEntries is the shared set of entries these tests use.
func sharedEntries() map[string]any {
	return map[string]any{
		"keep": map[string]any{"description": "shared keep", "inline": "echo shared keep"},
		"swap": map[string]any{"description": "shared swap", "inline": "echo shared swap", "confirm": true},
	}
}

func writeLocalFile(t *testing.T, e *testkit.Env, proj string, shortcuts map[string]any) {
	t.Helper()
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), docText(t, testkit.NS(shortcuts)), 0)
}

func localData(t *testing.T, proj string) map[string]any {
	t.Helper()
	return fileShortcuts(t, filepath.Join(proj, ".shortcuts.local"))
}

// overlaid is a project with a .shortcuts and a .shortcuts.local written by hand.
func overlaid(t *testing.T, e *testkit.Env) string {
	t.Helper()
	proj := e.Project(sharedEntries(), testkit.Dirs("src"))
	writeLocalFile(t, e, proj, map[string]any{"mine": testkit.Inline("echo mine"), "swap": testkit.Inline("echo local swap")})
	return proj
}

func TestProjectFiles_LocalAddsAndReplacesAWholeEntry(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	testkit.Eq(t, "keep", e.RunWith(in(proj), "r", ".", "keep").Stdout, "shared keep\n")
	testkit.Eq(t, "mine", e.RunWith(in(proj), "r", ".", "mine").Stdout, "mine\n")
	// The whole entry: the shared one's confirm doesn't carry over, so this needs no -y
	testkit.Eq(t, "swap", e.RunWith(in(proj), "r", ".", "swap").Stdout, "local swap\n")
}

func TestProjectFiles_RelativePathsResolveFromTheProjectRootInBothFiles(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"shared": map[string]any{"script": "bin/run.sh", "cwd": "."}},
		testkit.Dirs("src"), testkit.Files(map[string]any{"bin/run.sh": "#!/usr/bin/env bash\npwd\n"}), testkit.Mode(0o755))
	writeLocalFile(t, e, proj, map[string]any{"mine": map[string]any{"script": "bin/run.sh", "cwd": "src"}})
	src := filepath.Join(proj, "src")
	testkit.Eq(t, "shared", e.RunWith(in(src), "r", ".", "shared").Stdout, proj+"\n")
	testkit.Eq(t, "mine", e.RunWith(in(src), "r", ".", "mine").Stdout, src+"\n")
}

func TestProjectFiles_AProjectCanBeOnlyALocalFile(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(nil, testkit.Dirs("src"))
	testkit.Eq(t, "new", e.RunWith(in(proj), "new", "solo", "--local", "--", "echo solo").Code, 0)
	testkit.Eq(t, "run", e.RunWith(in(filepath.Join(proj, "src")), "r", ".", "solo").Stdout, "solo\n")
}

func TestProjectFiles_ImportsStayInTheSharedFile(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{})
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"),
		docText(t, map[string]any{"version": 1, "imports": map[string]any{"x": "x.shortcut"}, "shortcuts": map[string]any{}}), 0)
	testkit.Contains(t, "list", e.RunWith(in(proj), "list").Stderr, "can't have 'imports'")
}

func TestProjectFiles_NewLocalOverridesASharedNameAndSaysSo(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	res := e.RunWith(in(proj), "new", "keep", "--local", "--", "echo mine now")
	testkit.Contains(t, "stderr", res.Stderr, "this overrides 'keep'")
	testkit.Eq(t, "run", e.RunWith(in(proj), "r", ".", "keep").Stdout, "mine now\n")
}

func TestProjectFiles_NewLocalTakesACommandAndAddRecordsThePath(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{}, testkit.Files(map[string]any{"tools/run.sh": "#!/usr/bin/env bash\n"}), testkit.Mode(0o755))
	outside := e.Script("outside.sh", "#!/usr/bin/env bash\n")
	res := e.RunWith(in(proj), "new", "x", "--local", "--desc", "d")
	res.WantCode(t, 2)
	testkit.Contains(t, "stderr", res.Stderr, "after '--'")
	testkit.Eq(t, "in", e.RunWith(in(proj), "new", "in", "--script", "tools/run.sh", "--desc", "d", "--local").Code, 0)
	testkit.Eq(t, "out", e.RunWith(in(proj), "new", "out", "--script", outside, "--desc", "d", "--local").Code, 0)
	testkit.Eq(t, "in script", localData(t, proj)["in"].(map[string]any)["script"], "tools/run.sh")
	testkit.Eq(t, "out script", localData(t, proj)["out"].(map[string]any)["script"], outside)
	var names []string
	entries, _ := os.ReadDir(proj)
	for _, en := range entries {
		names = append(names, en.Name())
	}
	slices.Sort(names)
	testkit.Eq(t, "files", names, []string{".shortcuts", ".shortcuts.local", "tools"})
}

func TestProjectFiles_LocalOnlyWorksAtTheProjectRoot(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	src := filepath.Join(proj, "src")
	res := e.RunWith(in(src), "new", "x", "--local", "--", "true")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "is inside the project at")
	testkit.NotExist(t, filepath.Join(src, ".shortcuts.local"))
	testkit.Eq(t, "keep", e.RunWith(in(src), "r", ".", "keep").Stdout, "shared keep\n")
}

func TestProjectFiles_ANewLocalFileIsExcludedFromGitOnce(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{})
	e.RunCmd(testkit.Opts{}, "git", "init", "-q", proj)
	e.RunWith(in(proj), "new", "a", "--local", "--", "true")
	e.RunWith(in(proj), "new", "b", "--local", "--", "true")
	exclude := testkit.ReadFile(t, filepath.Join(proj, ".git", "info", "exclude"))
	n := 0
	for _, line := range strings.Split(exclude, "\n") {
		if line == ".shortcuts.local" {
			n++
		}
	}
	testkit.Eq(t, "exclude lines", n, 1)
	testkit.Eq(t, "status", e.RunCmd(testkit.Opts{Cwd: proj}, "git", "status", "--porcelain").Stdout, "?? .shortcuts\n")
}

func TestProjectFiles_OutsideGitNothingIsExcluded(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{})
	testkit.Eq(t, "new", e.RunWith(in(proj), "new", "a", "--local", "--", "true").Code, 0)
	testkit.NotExist(t, filepath.Join(proj, ".git"))
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
}

func TestProjectFiles_ListShowsEachEntrysSource(t *testing.T) {
	e := testkit.New(t)
	out := e.RunWith(in(overlaid(t, e)), "list").Stdout
	testkit.Matches(t, out, `keep +caller +shared `)
	testkit.Matches(t, out, `mine +caller +local `)
	testkit.Matches(t, out, `swap +caller +overridden `)
}

func TestProjectFiles_ShowNamesTheFileThatDefinesIt(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	res := e.RunWith(in(proj), "show", ".", "swap")
	testkit.Contains(t, "show", res.Stdout, "Defined in:  "+filepath.Join(proj, ".shortcuts.local"))
	testkit.Contains(t, "show", res.Stdout, "Source:      overridden")
}

func TestProjectFiles_TheCliNeverWritesTheSharedFile(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	before := testkit.ReadFile(t, filepath.Join(proj, ".shortcuts"))
	source := e.Script("tool.sh", "#!/usr/bin/env bash\n")
	for _, args := range [][]string{
		{"new", "a", "--local", "--", "echo a"},
		{"new", "b", "--script", source, "--desc", "d", "--local"},
		{"rm", "--local", "swap", "-y"},
	} {
		testkit.Eq(t, strings.Join(args, " "), e.RunWith(in(proj), args...).Code, 0)
	}
	testkit.Eq(t, "shared", testkit.ReadFile(t, filepath.Join(proj, ".shortcuts")), before)
}

func TestProjectFiles_RmWithoutLocalNeverReachesAProjectEntry(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	before := testkit.ReadFile(t, filepath.Join(proj, ".shortcuts.local"))
	for _, args := range [][]string{{"rm", "keep", "-y"}, {"rm", "mine", "-y"}} {
		res := e.RunWith(in(proj), args...)
		testkit.Eq(t, strings.Join(args, " "), res.Code, 1)
		testkit.Contains(t, strings.Join(args, " "), res.Stderr, "shortcut '"+args[1]+"' not found.")
	}
	testkit.Eq(t, "local file", testkit.ReadFile(t, filepath.Join(proj, ".shortcuts.local")), before)
}

func TestProjectFiles_RmLocalOfAnOverrideBringsTheSharedEntryBack(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	testkit.Eq(t, "rm", e.RunWith(in(proj), "rm", "--local", "swap", "-y").Code, 0)
	testkit.Eq(t, "run", e.RunWith(in(proj), "r", "-y", ".", "swap").Stdout, "shared swap\n")
}

func TestProjectFiles_RmLocalSaysTheSharedEntryAppliesAgain(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	rc, out := e.InTerminal(in(proj), []testkit.Key{{Wait: "[y/N]", Send: y("n\n")}}, "rm", "--local", "swap")
	testkit.Eq(t, "exit", rc, 1)
	testkit.Contains(t, "removes", out, "Removes your 'swap' from "+filepath.Join(proj, ".shortcuts.local")+".")
	testkit.Contains(t, "again", out, "The 'swap' in .shortcuts applies again.")
}

// An exclude file whose last line has no newline keeps that line intact.
func TestProjectFiles_ExcludeWithoutTrailingNewline(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{})
	e.RunCmd(testkit.Opts{}, "git", "init", "-q", proj)
	path := filepath.Join(proj, ".git", "info", "exclude")
	if err := os.WriteFile(path, []byte("build/"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.RunWith(in(proj), "new", "a", "--local", "--", "true")
	testkit.Eq(t, "exclude", testkit.ReadFile(t, path), "build/\n.shortcuts.local\n")
}

func TestProjectFiles_RmLocalOfANameOnlyInTheSharedFileFails(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	res := e.RunWith(in(proj), "rm", "--local", "keep", "-y")
	testkit.Eq(t, "exit", res.Code, 1)
	testkit.Contains(t, "err", res.Stderr, "'keep' isn't in .shortcuts.local, and shortcut doesn't change .shortcuts")
	testkit.Eq(t, "still shared", e.RunWith(in(proj), "r", ".", "keep").Stdout, "shared keep\n")
}

func TestProjectFiles_ANullLocalEntryIsAnInvalidEntry(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(sharedEntries())
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 1, "shortcuts": {"keep": null}}`, 0)
	for _, cmd := range []string{"show", "r"} {
		res := e.RunWith(in(proj), cmd, ".", "keep")
		testkit.Eq(t, cmd, res.Code, 1)
		testkit.Contains(t, cmd, res.Stderr+res.Stdout, "'keep' is not a valid entry: expected a JSON object")
	}
}
