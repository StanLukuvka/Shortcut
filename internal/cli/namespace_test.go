package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

func TestStores_LocalShortcutHidingGlobalWarns(t *testing.T) {
	e := testkit.New(t)
	fixed := e.Folder("fixed")
	e.WriteStore(e.Store, map[string]any{"penpot": map[string]any{"inline": "echo global", "cwd": fixed}})
	proj := e.Project(map[string]any{})
	// as new --local --cwd . writes it
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 2, "shortcuts": {"penpot": {"inline": "echo local", "cwd": "."}}}`, 0)
	update := runOK(t, e, in(proj), "update", "-y")
	testkit.Contains(t, "update", update.Stdout, "Inside this project, 'penpot' hides your global 'penpot'.")

	res := e.RunWith(in(proj), "run", "penpot")
	testkit.Eq(t, "local stdout", strings.TrimSpace(res.Stdout), "local")
	testkit.Eq(t, "local stderr", res.Stderr, "note: 'penpot' here is this project's; your global 'penpot' runs with: shortcut run --global penpot\n→ "+proj+"\n")
	testkit.Eq(t, "global", strings.TrimSpace(e.RunWith(in(proj), "run", "--global", "penpot").Stdout), "global")

	listing := e.RunWith(in(proj), "list").Stdout
	testkit.Contains(t, "list", listing, "[hides global]")
	testkit.Contains(t, "list", listing, "[hidden here by project]")

	res = e.RunWith(in(e.Tmp), "run", "penpot")
	testkit.Eq(t, "elsewhere stdout", strings.TrimSpace(res.Stdout), "global")
	testkit.Eq(t, "elsewhere stderr", res.Stderr, "→ "+fixed+"\n")
}

func TestStores_NamespaceDispatch(t *testing.T) {
	e := testkit.New(t)
	imports := filepath.Join(e.Store, "imports")
	e.WriteFile(filepath.Join(imports, "scripts", "del.sh"), "#!/usr/bin/env bash\necho \"deleting $1\"\n", 0)
	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{
		"delete": map[string]any{"script": "scripts/del.sh"},
		"where":  map[string]any{"inline": "pwd", "cwd": "."},
	}))
	runOK(t, e, testkit.Opts{}, "update", "docker", "-y")

	res := e.Run("r", "docker", "delete", "XYZ")
	testkit.Eq(t, "delete", res.Stdout, "deleting XYZ\n")
	testkit.Eq(t, "where", e.Run("r", "docker", "where").Stdout, imports+"\n")
	testkit.Eq(t, "location", e.Run("location", "docker", "delete").Stdout, imports+"/scripts/del.sh\n")
	testkit.Contains(t, "show", e.Run("show", "docker", "delete").Stdout, "Name:        docker delete")
	if !slices.Contains(strings.Fields(e.Run("list", "--names").Stdout), "docker") {
		t.Errorf("list --names has no docker")
	}
	testkit.Matches(t, e.Run("list").Stdout, `docker delete +caller`)

	res = e.Run("r", "docker")
	res.WantCode(t, 1)
	testkit.Contains(t, "namespace alone", res.Stderr, "'docker' is a namespace; run one of its shortcuts: delete, where")
	testkit.Contains(t, "nope", e.Run("r", "docker", "nope").Stderr, "'nope' not found in namespace 'docker'")
	// A name with words after it may be meant as a namespace: say where one would be
	res = e.Run("r", "podman", "list")
	res.WantCode(t, 1)
	testkit.Contains(t, "podman list", res.Stderr, "shortcut or namespace 'podman' not found. A global namespace is the file "+
		filepath.Join(realpath(t, e.Store), "imports", "podman.shortcut")+".")
	testkit.Contains(t, "podman", e.Run("r", "podman").Stderr, "shortcut 'podman' not found.")
}

func TestStores_NamespaceFromImportsKey(t *testing.T) {
	e := testkit.New(t)
	e.WriteFile(filepath.Join(e.Tmp, "shared", "tools.shortcut"), `{"version": 2, "shortcuts": {"hi": {"inline": "echo hi from tools"}}}`, 0)
	proj := e.Project(testkit.OWN, testkit.Extra(map[string]any{"imports": map[string]any{"tools": "../shared/tools.shortcut"}}), testkit.Approved())
	testkit.Eq(t, "tools hi", e.RunWith(in(proj), "r", "tools", "hi").Stdout, "hi from tools\n")
	testkit.Eq(t, "own", e.RunWith(in(proj), "r", "own").Stdout, "own\n")
}

func TestStores_NamespaceCollisionAndNestingErrors(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"docker": testkit.Inline("echo shortcut"), "ok": testkit.Inline("echo ok")})
	reg := filepath.Join(proj, ".shortcuts")
	e.WriteImport(reg, "docker", testkit.DockerPS)
	e.WriteStore(e.Store, map[string]any{"outside": testkit.Inline("echo outside")})
	e.RunWith(in(proj), "update", "-y") // approves what it can; the store itself can't run

	for _, words := range [][]string{{"docker"}, {"docker", "ps"}, {"ok"}} {
		res := e.RunWith(in(proj), append([]string{"r"}, words...)...)
		res.WantCode(t, 1)
		testkit.Contains(t, strings.Join(words, " "), res.Stderr, "'docker' is both a shortcut and a namespace")
	}
	// Only that store: names from a healthy one still run
	testkit.Eq(t, "outside", e.RunWith(in(proj), "r", "outside").Stdout, "outside\n")
	testkit.Contains(t, "list", e.RunWith(in(proj), "list").Stderr, "'docker' is both")

	e.WriteStore(reg, map[string]any{"ok": testkit.Inline("echo ok")})
	e.WriteImport(reg, "nested", map[string]any{"version": 2, "imports": map[string]any{"x": "x.shortcut"}, "shortcuts": map[string]any{}})
	res := e.RunWith(in(proj), "r", "nested", "x")
	res.WantCode(t, 1)
	testkit.Contains(t, "nested", res.Stderr, "namespaces don't nest")
}

func TestStores_ImportsMustBeShortcutFiles(t *testing.T) {
	e := testkit.New(t)
	e.WriteFile(filepath.Join(e.Store, "imports", "docker.json"), docText(t, testkit.DockerPS), 0)
	testkit.Contains(t, "docker.json", e.Run("r", "docker", "ps").Stderr, "'docker' not found")

	proj := e.Project(testkit.OWN, testkit.Extra(map[string]any{"imports": map[string]any{"tools": "tools.json"}}))
	e.RunWith(in(proj), "update", "-y")
	res := e.RunWith(in(proj), "r", "own")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "import 'tools'")
	testkit.Contains(t, "stderr", res.Stderr, "must be a .shortcut file")
}

func TestStores_ABareShortcutFileIsRefusedWithWhy(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hi": testkit.Inline("echo hi")})
	bare := e.WriteFile(filepath.Join(e.Store, "imports", ".shortcut"), docText(t, testkit.DockerPS), 0)
	res := e.Run("list")
	testkit.Contains(t, "list", res.Stderr, "warning: "+bare+": a namespace file needs a name, e.g. docker.shortcut")
	if strings.Contains(res.Stderr, "invalid shortcut name") {
		t.Errorf("wrong reason: %s", res.Stderr)
	}
	testkit.Eq(t, "hi", e.Run("r", "hi").Stdout, "hi\n")
	os.Remove(bare)

	proj := e.Project(testkit.OWN, testkit.Extra(map[string]any{"imports": map[string]any{"x": ".shortcut"}}),
		testkit.Files(map[string]any{".shortcut": docText(t, testkit.DockerPS)}))
	e.RunWith(in(proj), "update", "-y")
	for _, cmd := range [][]string{{"r", "x", "ps"}, {"r", "own"}} {
		res := e.RunWith(in(proj), cmd...)
		res.WantCode(t, 1)
		testkit.Contains(t, "import", res.Stderr, "import 'x'")
		testkit.Contains(t, "import", res.Stderr, "a namespace file needs a name, e.g. docker.shortcut")
	}
	testkit.Eq(t, "hi", e.RunWith(in(proj), "r", "hi").Stdout, "hi\n")
}

func TestStores_AProjectNamespaceOverAGlobalOneIsAnnounced(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{
		"ps": testkit.Inline("echo global-ps"), "delete": testkit.Inline("echo global-delete")}))
	runOK(t, e, testkit.Opts{}, "update", "docker", "-y")
	proj := e.Project(map[string]any{})
	e.WriteImport(filepath.Join(proj, ".shortcuts"), "docker", testkit.NS(map[string]any{"delete": testkit.Inline("echo project-delete")}))

	update := runOK(t, e, in(proj), "update", "-y")
	testkit.Contains(t, "update", update.Stdout, "Inside this project, 'docker' hides your global 'docker'.")
	o := in(proj)
	o.Env = map[string]string{"SHORTCUT_SHOW_DIR": "0"}
	res := e.RunWith(o, "r", "docker", "delete")
	testkit.Eq(t, "stdout", res.Stdout, "project-delete\n")
	testkit.Eq(t, "stderr", res.Stderr, "note: 'docker' here is this project's; your global 'docker' runs with: shortcut run --global docker delete\n")
	res = e.RunWith(in(proj), "r", "docker", "ps")
	res.WantCode(t, 1)
	testkit.Contains(t, "ps", res.Stderr, "Your global 'docker' has it: shortcut r --global docker ps")
	testkit.Contains(t, "list", e.RunWith(in(proj), "list").Stdout,
		"Namespace docker ("+filepath.Join(proj, "imports", "docker.shortcut")+")  [hides global]")
}

func TestStores_AProjectShortcutOverAGlobalNamespaceIsAnnounced(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "docker", testkit.DockerPS)
	proj := e.Project(map[string]any{"docker": testkit.Inline("echo project")})
	testkit.Contains(t, "update", runOK(t, e, in(proj), "update", "-y").Stdout, "Inside this project, 'docker' hides your global 'docker'.")
	testkit.Contains(t, "run", e.RunWith(in(proj), "r", "docker").Stderr, "note: 'docker' here is this project's")
}

func TestStores_BrokenProjectStopsEveryRunInsideIt(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"outside": testkit.Inline("echo outside")})
	proj := e.BrokenProject()
	registry := filepath.Join(proj, ".shortcuts")

	for _, name := range []string{"outside", "nothing"} {
		res := e.RunWith(in(proj), "r", name)
		if res.Code != 1 || res.Stdout != "" {
			t.Errorf("%s: exit %d stdout %q", name, res.Code, res.Stdout)
		}
		testkit.Contains(t, name, res.Stderr, "corrupt registry "+registry)
		testkit.Contains(t, name, res.Stderr, ". Nothing runs inside this project until it's fixed; for your own: shortcut r --global "+name+"\n")
	}
	testkit.Eq(t, "--global", e.RunWith(in(proj), "r", "--global", "outside").Stdout, "outside\n")

	// Looking is not running: show and location read past the file, quietly
	for _, cmd := range [][]string{{"show", "outside"}, {"show", "--json", "outside"}, {"location", "outside"}} {
		res := e.RunWith(in(proj), cmd...)
		if strings.Contains(res.Stderr, "corrupt registry") || strings.Contains(res.Stderr, "warning") {
			t.Errorf("%v: stderr %q", cmd, res.Stderr)
		}
	}
	testkit.Contains(t, "show", e.RunWith(in(proj), "show", "outside").Stdout, "Scope:       global")
	testkit.Contains(t, "show nothing", e.RunWith(in(proj), "show", "nothing").Stderr, "'nothing' not found")

	// rm doesn't reach past the broken file either; --global does
	testkit.Eq(t, "rm", e.RunWith(in(proj), "rm", "outside", "-y").Code, 1)
	if _, has := e.LoadGlobal()["outside"]; !has {
		t.Error("rm reached past the broken project")
	}
	testkit.Eq(t, "rm --global", e.RunWith(in(proj), "rm", "--global", "outside", "-y").Code, 0)
	if _, has := e.LoadGlobal()["outside"]; has {
		t.Error("rm --global left it")
	}
	testkit.Contains(t, "list", e.RunWith(in(proj), "list").Stderr, "corrupt registry")
	if code := e.RunWith(in(proj), "update").Code; code != 1 {
		t.Errorf("update: exit %d", code)
	}
}

func TestStores_ARepairedProjectRunsAgain(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"build": testkit.Inline("echo global")})
	proj := e.Project(map[string]any{"build": testkit.Inline("echo project")}, testkit.Approved())
	registry := filepath.Join(proj, ".shortcuts")
	good := testkit.ReadFile(t, registry)
	e.WriteFile(registry, "<<<<<<< HEAD\n"+good, 0)
	res := e.RunWith(in(proj), "r", "build")
	res.WantCode(t, 1)
	testkit.Contains(t, "broken", res.Stderr, "Nothing runs inside this project")
	testkit.Eq(t, "--global", e.RunWith(in(proj), "r", "--global", "build").Stdout, "global\n")
	e.WriteFile(registry, good, 0)
	testkit.Eq(t, "repaired", e.RunWith(in(proj), "r", "build").Stdout, "project\n")
}

func TestStores_BrokenProjectRefusesANamespaceItImported(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"docker": testkit.Inline("echo global")})
	proj := e.Project(map[string]any{},
		testkit.Extra(map[string]any{"imports": map[string]any{"docker": "../tools/docker.shortcut"}}),
		testkit.Files(map[string]any{"tools/docker.shortcut": `{"version": 2, "shortcuts": {"ps": {"inline": "true"}}}`}))
	runOK(t, e, in(proj), "update", ".", "-y")
	e.CorruptStore(filepath.Join(proj, ".shortcuts"))

	res := e.RunWith(in(proj), "r", "docker", "ps")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "for your own: shortcut r --global docker")
	// The file can't be read, so its imports are gone too: show finds the global 'docker'
	testkit.Contains(t, "show", e.RunWith(in(proj), "show", "docker").Stdout, "Scope:       global")
}

func TestStores_AnUnknownTopLevelFieldStopsTheProjectToo(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"outside": testkit.Inline("echo outside")})
	proj := e.Project(map[string]any{"inside": testkit.Inline("true"), "mine": testkit.Inline("true")}, testkit.Approved())
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 2, "shortcuts": {"mine": {"inline": "echo mine"}}}`, 0)
	registry := filepath.Join(proj, ".shortcuts")
	e.WriteStore(registry, map[string]any{"inside": testkit.Inline("true"), "mine": testkit.Inline("true")}, map[string]any{"bogus": 1})

	for _, name := range []string{"inside", "mine", "outside"} {
		res := e.RunWith(in(proj), "r", name)
		res.WantCode(t, 1)
		testkit.Contains(t, name, res.Stderr, registry+" has an unknown field: bogus. Nothing runs inside this project")
	}

	// The file still parses, so show finds its entries
	res := e.RunWith(in(proj), "show", "inside")
	res.WantCode(t, 0)
	testkit.Contains(t, "show", res.Stdout, "Scope:       project")
	testkit.Contains(t, "show", res.Stdout, "Approval:    changed")
	// show and rm --local find its entries
	testkit.Eq(t, "rm inside", e.RunWith(in(proj), "rm", "inside", "-y").Code, 1) // a project entry, as ever
	res = e.RunWith(in(proj), "rm", "--local", "mine", "-y")
	res.WantCode(t, 0)
	if _, has := localData(t, proj)["mine"]; has {
		t.Error("mine is still in .shortcuts.local")
	}
	testkit.Contains(t, "registry", testkit.ReadFile(t, registry), `"bogus":1`)
}

func TestStores_BrokenGlobalStoreStillReports(t *testing.T) {
	e := testkit.New(t)
	e.CorruptStore(e.Store)
	res := e.Run("r", "anything")
	res.WantCode(t, 1)
	testkit.Contains(t, "run", res.Stderr, "corrupt registry")
	for _, bad := range []string{"panic", "goroutine"} {
		if strings.Contains(res.Stderr, bad) {
			t.Errorf("stderr has %q", bad)
		}
	}
	testkit.Contains(t, "list", e.Run("list").Stderr, "corrupt registry")
}

func TestStores_AFifoScriptNeverBlocks(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"p": map[string]any{"script": "pipe"}, "q": testkit.Inline("true")}, testkit.Files(map[string]any{"pipe": "#!/bin/sh\n"}))
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
	testkit.Contains(t, "list", e.RunWith(o, "list").Stderr, "script not found")
}

func docText(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
