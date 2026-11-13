// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-3). Don't hand-edit without checking the plan.

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func realpath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// py: test_stores.py::test_local_shortcut_hiding_global_warns
func TestStores_LocalShortcutHidingGlobalWarns(t *testing.T) {
	e := testkit.New(t)
	fixed := e.Folder("fixed")
	e.WriteStore(e.Store, map[string]any{"penpot": map[string]any{"inline": "echo global", "cwd": fixed}})
	proj := e.Project(map[string]any{})
	// as new --local --cwd . writes it
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 2, "shortcuts": {"penpot": {"inline": "echo local", "cwd": "."}}}`, 0)
	update := runOK(t, e, in(proj), "update", "-y")
	contains(t, "update", update.Stdout, "Inside this project, 'penpot' hides your global 'penpot'.")

	res := e.RunWith(in(proj), "run", "penpot")
	eq(t, "local stdout", strings.TrimSpace(res.Stdout), "local")
	eq(t, "local stderr", res.Stderr, "note: 'penpot' here is this project's; your global 'penpot' runs with: shortcut run --global penpot\n→ "+proj+"\n")
	eq(t, "global", strings.TrimSpace(e.RunWith(in(proj), "run", "--global", "penpot").Stdout), "global")

	listing := e.RunWith(in(proj), "list").Stdout
	contains(t, "list", listing, "[hides global]")
	contains(t, "list", listing, "[hidden here by project]")

	res = e.RunWith(in(e.Tmp), "run", "penpot")
	eq(t, "elsewhere stdout", strings.TrimSpace(res.Stdout), "global")
	eq(t, "elsewhere stderr", res.Stderr, "→ "+fixed+"\n")
}

// py: test_stores.py::test_namespace_dispatch
func TestStores_NamespaceDispatch(t *testing.T) {
	e := testkit.New(t)
	imports := filepath.Join(e.Store, "imports")
	e.WriteFile(filepath.Join(imports, "scripts", "del.sh"), "#!/usr/bin/env bash\necho \"deleting $1\"\n", 0)
	e.WriteImport(e.Store, "docker", map[string]any{"version": 2, "shortcuts": map[string]any{
		"delete": map[string]any{"script": "scripts/del.sh"},
		"where":  map[string]any{"inline": "pwd", "cwd": "."},
	}})
	runOK(t, e, testkit.Opts{}, "update", "docker", "-y")

	res := e.Run("r", "docker", "delete", "XYZ")
	eq(t, "delete", res.Stdout, "deleting XYZ\n")
	eq(t, "where", e.Run("r", "docker", "where").Stdout, imports+"\n")
	eq(t, "location", e.Run("location", "docker", "delete").Stdout, imports+"/scripts/del.sh\n")
	contains(t, "show", e.Run("show", "docker", "delete").Stdout, "Name:        docker delete")
	if !contains2(strings.Fields(e.Run("list", "--names").Stdout), "docker") {
		t.Errorf("list --names has no docker")
	}
	matches(t, e.Run("list").Stdout, `docker delete +caller`)

	res = e.Run("r", "docker")
	if res.Code != 1 {
		t.Errorf("namespace alone: exit %d", res.Code)
	}
	contains(t, "namespace alone", res.Stderr, "'docker' is a namespace; run one of its shortcuts: delete, where")
	contains(t, "nope", e.Run("r", "docker", "nope").Stderr, "'nope' not found in namespace 'docker'")
	// A name with words after it may be meant as a namespace: say where one would be
	res = e.Run("r", "podman", "list")
	if res.Code != 1 {
		t.Errorf("podman list: exit %d", res.Code)
	}
	contains(t, "podman list", res.Stderr, "shortcut or namespace 'podman' not found. A global namespace is the file "+
		filepath.Join(realpath(t, e.Store), "imports", "podman.shortcut")+".")
	contains(t, "podman", e.Run("r", "podman").Stderr, "shortcut 'podman' not found.")
}

func contains2(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// py: test_stores.py::test_namespace_from_imports_key
func TestStores_NamespaceFromImportsKey(t *testing.T) {
	e := testkit.New(t)
	e.WriteFile(filepath.Join(e.Tmp, "shared", "tools.shortcut"), `{"version": 2, "shortcuts": {"hi": {"inline": "echo hi from tools"}}}`, 0)
	proj := e.Project(testkit.OWN, testkit.Extra(map[string]any{"imports": map[string]any{"tools": "../shared/tools.shortcut"}}), testkit.Approved())
	eq(t, "tools hi", e.RunWith(in(proj), "r", "tools", "hi").Stdout, "hi from tools\n")
	eq(t, "own", e.RunWith(in(proj), "r", "own").Stdout, "own\n")
}

// py: test_stores.py::test_namespace_collision_and_nesting_errors
func TestStores_NamespaceCollisionAndNestingErrors(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"docker": inline("echo shortcut"), "ok": inline("echo ok")})
	reg := filepath.Join(proj, ".shortcuts")
	e.WriteImport(reg, "docker", testkit.DockerPS)
	e.WriteStore(e.Store, map[string]any{"outside": inline("echo outside")})
	e.RunWith(in(proj), "update", "-y") // approves what it can; the store itself can't run

	for _, words := range [][]string{{"docker"}, {"docker", "ps"}, {"ok"}} {
		res := e.RunWith(in(proj), append([]string{"r"}, words...)...)
		if res.Code != 1 {
			t.Errorf("%v: exit %d", words, res.Code)
		}
		contains(t, strings.Join(words, " "), res.Stderr, "'docker' is both a shortcut and a namespace")
	}
	// Only that store: names from a healthy one still run
	eq(t, "outside", e.RunWith(in(proj), "r", "outside").Stdout, "outside\n")
	contains(t, "list", e.RunWith(in(proj), "list").Stderr, "'docker' is both")

	e.WriteStore(reg, map[string]any{"ok": inline("echo ok")})
	e.WriteImport(reg, "nested", map[string]any{"version": 2, "imports": map[string]any{"x": "x.shortcut"}, "shortcuts": map[string]any{}})
	res := e.RunWith(in(proj), "r", "nested", "x")
	if res.Code != 1 {
		t.Errorf("nested: exit %d", res.Code)
	}
	contains(t, "nested", res.Stderr, "namespaces don't nest")
}

// py: test_stores.py::test_imports_must_be_shortcut_files
func TestStores_ImportsMustBeShortcutFiles(t *testing.T) {
	e := testkit.New(t)
	e.WriteFile(filepath.Join(e.Store, "imports", "docker.json"), docText(t, testkit.DockerPS), 0)
	contains(t, "docker.json", e.Run("r", "docker", "ps").Stderr, "'docker' not found")

	proj := e.Project(testkit.OWN, testkit.Extra(map[string]any{"imports": map[string]any{"tools": "tools.json"}}))
	e.RunWith(in(proj), "update", "-y")
	res := e.RunWith(in(proj), "r", "own")
	if res.Code != 1 {
		t.Errorf("exit %d", res.Code)
	}
	contains(t, "stderr", res.Stderr, "import 'tools'")
	contains(t, "stderr", res.Stderr, "must be a .shortcut file")
}

// py: test_stores.py::test_a_bare_shortcut_file_is_refused_with_why
func TestStores_ABareShortcutFileIsRefusedWithWhy(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hi": inline("echo hi")})
	bare := e.WriteFile(filepath.Join(e.Store, "imports", ".shortcut"), docText(t, testkit.DockerPS), 0)
	res := e.Run("list")
	contains(t, "list", res.Stderr, "warning: "+bare+": a namespace file needs a name, e.g. docker.shortcut")
	if strings.Contains(res.Stderr, "invalid shortcut name") {
		t.Errorf("wrong reason: %s", res.Stderr)
	}
	eq(t, "hi", e.Run("r", "hi").Stdout, "hi\n")
	os.Remove(bare)

	proj := e.Project(testkit.OWN, testkit.Extra(map[string]any{"imports": map[string]any{"x": ".shortcut"}}),
		testkit.Files(map[string]any{".shortcut": docText(t, testkit.DockerPS)}))
	e.RunWith(in(proj), "update", "-y")
	for _, cmd := range [][]string{{"r", "x", "ps"}, {"r", "own"}} {
		res := e.RunWith(in(proj), cmd...)
		if res.Code != 1 {
			t.Errorf("%v: exit %d", cmd, res.Code)
		}
		contains(t, "import", res.Stderr, "import 'x'")
		contains(t, "import", res.Stderr, "a namespace file needs a name, e.g. docker.shortcut")
	}
	eq(t, "hi", e.RunWith(in(proj), "r", "hi").Stdout, "hi\n")
}

// py: test_stores.py::test_shortcut_file_needs_the_current_version
func TestStores_ShortcutFileNeedsTheCurrentVersion(t *testing.T) {
	e := testkit.New(t)
	path := e.WriteImport(e.Store, "docker", map[string]any{"shortcuts": map[string]any{"ps": inline("echo ps")}})
	res := e.Run("update", "docker", "-y")
	if res.Code != 1 {
		t.Errorf("no version: exit %d", res.Code)
	}
	contains(t, "no version", res.Stderr, `needs "version": 2`)

	e.WriteFile(path, `{"version": 3, "shortcuts": {"ps": {"inline": "echo ps"}}}`, 0)
	res = e.Run("update", "docker", "-y")
	if res.Code != 1 {
		t.Errorf("version 3: exit %d", res.Code)
	}
	contains(t, "version 3", res.Stderr, "newer than this shortcut reads; upgrade shortcut")
}

// py: test_stores.py::test_a_project_namespace_over_a_global_one_is_announced
func TestStores_AProjectNamespaceOverAGlobalOneIsAnnounced(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "docker", map[string]any{"version": 2, "shortcuts": map[string]any{
		"ps": inline("echo global-ps"), "delete": inline("echo global-delete")}})
	runOK(t, e, testkit.Opts{}, "update", "docker", "-y")
	proj := e.Project(map[string]any{})
	e.WriteImport(filepath.Join(proj, ".shortcuts"), "docker", map[string]any{"version": 2, "shortcuts": map[string]any{"delete": inline("echo project-delete")}})

	update := runOK(t, e, in(proj), "update", "-y")
	contains(t, "update", update.Stdout, "Inside this project, 'docker' hides your global 'docker'.")
	o := in(proj)
	o.Env = map[string]string{"SHORTCUT_SHOW_DIR": "0"}
	res := e.RunWith(o, "r", "docker", "delete")
	eq(t, "stdout", res.Stdout, "project-delete\n")
	eq(t, "stderr", res.Stderr, "note: 'docker' here is this project's; your global 'docker' runs with: shortcut run --global docker delete\n")
	res = e.RunWith(in(proj), "r", "docker", "ps")
	if res.Code != 1 {
		t.Errorf("ps: exit %d", res.Code)
	}
	contains(t, "ps", res.Stderr, "Your global 'docker' has it: shortcut r --global docker ps")
	contains(t, "list", e.RunWith(in(proj), "list").Stdout,
		"Namespace docker ("+filepath.Join(proj, "imports", "docker.shortcut")+")  [hides global]")
}

// py: test_stores.py::test_a_project_shortcut_over_a_global_namespace_is_announced
func TestStores_AProjectShortcutOverAGlobalNamespaceIsAnnounced(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "docker", testkit.DockerPS)
	proj := e.Project(map[string]any{"docker": inline("echo project")})
	contains(t, "update", runOK(t, e, in(proj), "update", "-y").Stdout, "Inside this project, 'docker' hides your global 'docker'.")
	contains(t, "run", e.RunWith(in(proj), "r", "docker").Stderr, "note: 'docker' here is this project's")
}

// py: test_stores.py::test_broken_project_stops_every_run_inside_it
func TestStores_BrokenProjectStopsEveryRunInsideIt(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"outside": inline("echo outside")})
	proj := e.BrokenProject()
	registry := filepath.Join(proj, ".shortcuts")

	for _, name := range []string{"outside", "nothing"} {
		res := e.RunWith(in(proj), "r", name)
		if res.Code != 1 || res.Stdout != "" {
			t.Errorf("%s: exit %d stdout %q", name, res.Code, res.Stdout)
		}
		contains(t, name, res.Stderr, "corrupt registry "+registry)
		contains(t, name, res.Stderr, ". Nothing runs inside this project until it's fixed; for your own: shortcut r --global "+name+"\n")
	}
	eq(t, "--global", e.RunWith(in(proj), "r", "--global", "outside").Stdout, "outside\n")

	// Looking is not running: show and location read past the file, quietly
	for _, cmd := range [][]string{{"show", "outside"}, {"show", "--json", "outside"}, {"location", "outside"}} {
		res := e.RunWith(in(proj), cmd...)
		if strings.Contains(res.Stderr, "corrupt registry") || strings.Contains(res.Stderr, "warning") {
			t.Errorf("%v: stderr %q", cmd, res.Stderr)
		}
	}
	contains(t, "show", e.RunWith(in(proj), "show", "outside").Stdout, "Scope:       global")
	contains(t, "show nothing", e.RunWith(in(proj), "show", "nothing").Stderr, "'nothing' not found")

	// rm doesn't reach past the broken file either; --global does
	eq(t, "rm", e.RunWith(in(proj), "rm", "outside", "-y").Code, 1)
	if _, has := e.LoadGlobal()["outside"]; !has {
		t.Error("rm reached past the broken project")
	}
	eq(t, "rm --global", e.RunWith(in(proj), "rm", "--global", "outside", "-y").Code, 0)
	if _, has := e.LoadGlobal()["outside"]; has {
		t.Error("rm --global left it")
	}
	contains(t, "list", e.RunWith(in(proj), "list").Stderr, "corrupt registry")
	if code := e.RunWith(in(proj), "update").Code; code != 1 {
		t.Errorf("update: exit %d", code)
	}
}

// py: test_stores.py::test_a_repaired_project_runs_again
func TestStores_ARepairedProjectRunsAgain(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"build": inline("echo global")})
	proj := e.Project(map[string]any{"build": inline("echo project")}, testkit.Approved())
	registry := filepath.Join(proj, ".shortcuts")
	good := readFile(t, registry)
	e.WriteFile(registry, "<<<<<<< HEAD\n"+good, 0)
	res := e.RunWith(in(proj), "r", "build")
	if res.Code != 1 {
		t.Errorf("broken: exit %d", res.Code)
	}
	contains(t, "broken", res.Stderr, "Nothing runs inside this project")
	eq(t, "--global", e.RunWith(in(proj), "r", "--global", "build").Stdout, "global\n")
	e.WriteFile(registry, good, 0)
	eq(t, "repaired", e.RunWith(in(proj), "r", "build").Stdout, "project\n")
}

// py: test_stores.py::test_broken_project_refuses_a_namespace_it_imported
func TestStores_BrokenProjectRefusesANamespaceItImported(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"docker": inline("echo global")})
	proj := e.Project(map[string]any{},
		testkit.Extra(map[string]any{"imports": map[string]any{"docker": "../tools/docker.shortcut"}}),
		testkit.Files(map[string]any{"tools/docker.shortcut": `{"version": 2, "shortcuts": {"ps": {"inline": "true"}}}`}))
	runOK(t, e, in(proj), "update", ".", "-y")
	e.CorruptStore(filepath.Join(proj, ".shortcuts"))

	res := e.RunWith(in(proj), "r", "docker", "ps")
	if res.Code != 1 {
		t.Errorf("exit %d", res.Code)
	}
	contains(t, "stderr", res.Stderr, "for your own: shortcut r --global docker")
	// The file can't be read, so its imports are gone too: show finds the global 'docker'
	contains(t, "show", e.RunWith(in(proj), "show", "docker").Stdout, "Scope:       global")
}

// py: test_stores.py::test_an_unknown_top_level_field_stops_the_project_too
func TestStores_AnUnknownTopLevelFieldStopsTheProjectToo(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"outside": inline("echo outside")})
	proj := e.Project(map[string]any{"inside": inline("true"), "mine": inline("true")}, testkit.Approved())
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 2, "shortcuts": {"mine": {"inline": "echo mine"}}}`, 0)
	registry := filepath.Join(proj, ".shortcuts")
	e.WriteStore(registry, map[string]any{"inside": inline("true"), "mine": inline("true")}, map[string]any{"bogus": 1})

	for _, name := range []string{"inside", "mine", "outside"} {
		res := e.RunWith(in(proj), "r", name)
		if res.Code != 1 {
			t.Errorf("%s: exit %d", name, res.Code)
		}
		contains(t, name, res.Stderr, registry+" has an unknown field: bogus. Nothing runs inside this project")
	}

	// The file still parses, so show finds its entries
	res := e.RunWith(in(proj), "show", "inside")
	if res.Code != 0 {
		t.Errorf("show: exit %d %s", res.Code, res.Stderr)
	}
	contains(t, "show", res.Stdout, "Scope:       project")
	contains(t, "show", res.Stdout, "Approval:    changed")
	// show and rm --local find its entries
	eq(t, "rm inside", e.RunWith(in(proj), "rm", "inside", "-y").Code, 1) // a project entry, as ever
	res = e.RunWith(in(proj), "rm", "--local", "mine", "-y")
	if res.Code != 0 {
		t.Errorf("rm --local: exit %d: %s", res.Code, res.Stderr)
	}
	if _, has := localData(t, proj)["mine"]; has {
		t.Error("mine is still in .shortcuts.local")
	}
	contains(t, "registry", readFile(t, registry), `"bogus":1`)
}

// py: test_stores.py::test_broken_global_store_still_reports
func TestStores_BrokenGlobalStoreStillReports(t *testing.T) {
	e := testkit.New(t)
	e.CorruptStore(e.Store)
	res := e.Run("r", "anything")
	if res.Code != 1 {
		t.Errorf("exit %d", res.Code)
	}
	contains(t, "run", res.Stderr, "corrupt registry")
	for _, bad := range []string{"panic", "goroutine"} {
		if strings.Contains(res.Stderr, bad) {
			t.Errorf("stderr has %q", bad)
		}
	}
	contains(t, "list", e.Run("list").Stderr, "corrupt registry")
}

// py: test_stores.py::test_a_fifo_script_never_blocks
func TestStores_AFifoScriptNeverBlocks(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"p": map[string]any{"script": "pipe"}, "q": inline("true")}, testkit.Files(map[string]any{"pipe": "#!/bin/sh\n"}))
	runOK(t, e, in(proj), "update", ".", "-y")
	os.Remove(filepath.Join(proj, "pipe"))
	if err := syscall.Mkfifo(filepath.Join(proj, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := in(proj)
	o.Timeout = 10 * time.Second
	for _, cmd := range [][]string{{"list"}, {"list", "--names"}, {"r", "q"}, {"update", ".", "-y"}} {
		res := e.RunWith(o, cmd...)
		if strings.Contains(res.Stderr, "panic") || strings.Contains(res.Stderr, "goroutine") {
			t.Errorf("%v: %s", cmd, res.Stderr)
		}
	}
	contains(t, "list", e.RunWith(o, "list").Stderr, "script not found")
}

// py: test_stores.py::test_a_version_that_isnt_a_whole_number_is_refused
func TestStores_AVersionThatIsntAWholeNumberIsRefused(t *testing.T) {
	e := testkit.New(t)
	for _, version := range []string{`"3"`, "3.0", "null", "true"} {
		e.WriteRegistryRaw(e.Store, `{"version": `+version+`, "shortcuts": {"v": {"inline": "echo ran"}}}`)
		res := e.Run("r", "v")
		if res.Code != 1 || res.Stdout != "" {
			t.Errorf("%s: exit %d stdout %q", version, res.Code, res.Stdout)
		}
		contains(t, version, res.Stderr, "'version' must be a whole number, such as 2")
	}
	proj := e.Project(testkit.GO, testkit.Extra(map[string]any{"version": "3"}))
	res := e.RunWith(in(proj), "update", "-y")
	if res.Code != 1 {
		t.Errorf("project: exit %d", res.Code)
	}
	contains(t, "project", res.Stderr, "'version' must be a whole number")
}

// py: test_stores.py::test_args_need_a_name_that_is_a_string
func TestStores_ArgsNeedANameThatIsAString(t *testing.T) {
	e := testkit.New(t)
	for _, arg := range []map[string]any{{"required": true}, {"name": nil}, {"name": true}} {
		entry := map[string]any{"inline": "echo hi", "args": []any{arg}}
		e.WriteImport(e.Store, "ns", map[string]any{"version": 2, "shortcuts": map[string]any{"go": entry}})
		res := e.Run("update", "ns", "-y")
		raw, _ := json.Marshal(arg)
		if res.Code != 1 {
			t.Errorf("%s: exit %d", raw, res.Code)
		}
		contains(t, string(raw), res.Stderr, "needs a 'name'")
	}
}

func docText(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
