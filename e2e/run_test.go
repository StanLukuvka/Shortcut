package e2e

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestStores_NamespaceDispatch(t *testing.T) {
	e := testkit.New(t)
	imports := filepath.Join(e.Store, "imports")
	e.WriteFile(filepath.Join(imports, "scripts", "del.sh"), bashShebang+"echo \"deleting $1\"\n", 0o755)
	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{
		"delete": map[string]any{"script": "scripts/del.sh"},
		"where":  map[string]any{"inline": "pwd", "cwd": "."},
	}))

	res := e.Run("r", "docker", "delete", "XYZ")
	testkit.Eq(t, "delete", res.Stdout, "deleting XYZ\n")
	testkit.Eq(t, "where", e.Run("r", "docker", "where").Stdout, imports+"\n")
	testkit.Eq(t, "script_path", e.Run("show", "--field", "script_path", "docker", "delete").Stdout, imports+"/scripts/del.sh\n")
	testkit.Contains(t, "show", e.Run("show", "docker", "delete").Stdout, "Name:        docker delete")
	if !slices.Contains(strings.Fields(e.Run("list", "--names").Stdout), "docker") {
		t.Errorf("list --names has no docker")
	}
	testkit.Matches(t, e.Run("list").Stdout, `docker delete +caller`)

	res = e.Run("r", "docker")
	res.WantCode(t, 1)
	testkit.Contains(t, "namespace alone", res.Stderr, "'docker' is a namespace; run one of its shortcuts")
	testkit.Contains(t, "nope", e.Run("r", "docker", "nope").Stderr, "'nope' not found in namespace 'docker'")
	res = e.Run("r", "podman", "list")
	res.WantCode(t, 1)
	testkit.Contains(t, "podman list", res.Stderr, "shortcut or namespace 'podman' not found.")
	testkit.Contains(t, "podman", e.Run("r", "podman").Stderr, "shortcut 'podman' not found.")
}

func TestStores_NamespaceFromImportsKey(t *testing.T) {
	e := testkit.New(t)
	e.WriteFile(filepath.Join(e.Tmp, "shared", "tools.shortcut"), `{"version": 1, "shortcuts": {"hi": {"inline": "echo hi from tools"}}}`, 0)
	proj := e.Project(testkit.OWN, testkit.Extra(map[string]any{"imports": map[string]any{"tools": "../shared/tools.shortcut"}}))
	testkit.Eq(t, "tools hi", e.RunWith(in(proj), "r", ".", "tools", "hi").Stdout, "hi from tools\n")
	testkit.Eq(t, "own", e.RunWith(in(proj), "r", ".", "own").Stdout, "own\n")
}

func TestStores_NamespaceCollisionAndNestingErrors(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"docker": testkit.Inline("echo shortcut"), "ok": testkit.Inline("echo ok")})
	reg := filepath.Join(proj, ".shortcuts")
	e.WriteImport(reg, "docker", testkit.DockerPS)
	e.WriteStore(e.Store, map[string]any{"outside": testkit.Inline("echo outside")})

	for _, words := range [][]string{{"docker"}, {"docker", "ps"}, {"ok"}} {
		res := e.RunWith(in(proj), append([]string{"r", "."}, words...)...)
		res.WantCode(t, 1)
		testkit.Contains(t, strings.Join(words, " "), res.Stderr, "'docker' is both a shortcut and a namespace")
	}
	// Only that store: names from a healthy one still run
	testkit.Eq(t, "outside", e.RunWith(in(proj), "r", "outside").Stdout, "outside\n")
	testkit.Contains(t, "list", e.RunWith(in(proj), "list").Stderr, "'docker' is both")

	e.WriteStore(reg, map[string]any{"ok": testkit.Inline("echo ok")})
	e.WriteImport(reg, "nested", map[string]any{"version": 1, "imports": map[string]any{"x": "x.shortcut"}, "shortcuts": map[string]any{}})
	res := e.RunWith(in(proj), "r", ".", "nested", "x")
	res.WantCode(t, 1)
	testkit.Contains(t, "nested", res.Stderr, "namespaces don't nest")
}

func TestStores_ImportsMustBeShortcutFiles(t *testing.T) {
	e := testkit.New(t)
	e.WriteFile(filepath.Join(e.Store, "imports", "docker.json"), docText(t, testkit.DockerPS), 0)
	testkit.Contains(t, "docker.json", e.Run("r", "docker", "ps").Stderr, "'docker' not found")

	proj := e.Project(testkit.OWN, testkit.Extra(map[string]any{"imports": map[string]any{"tools": "tools.json"}}))
	res := e.RunWith(in(proj), "r", ".", "own")
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
		testkit.Files(map[string]string{".shortcut": docText(t, testkit.DockerPS)}))
	for _, cmd := range [][]string{{"r", ".", "x", "ps"}, {"r", ".", "own"}} {
		res := e.RunWith(in(proj), cmd...)
		res.WantCode(t, 1)
		testkit.Contains(t, "import", res.Stderr, "import 'x'")
		testkit.Contains(t, "import", res.Stderr, "a namespace file needs a name, e.g. docker.shortcut")
	}
	testkit.Eq(t, "hi", e.RunWith(in(proj), "r", "hi").Stdout, "hi\n")
}

func TestStores_BrokenProjectStopsEveryRunInsideIt(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"outside": testkit.Inline("echo outside")})
	proj := e.BrokenProject()
	registry := filepath.Join(proj, ".shortcuts")

	for _, name := range []string{"outside", "nothing"} {
		res := e.RunWith(in(proj), "r", ".", name)
		if res.Code != 1 || res.Stdout != "" {
			t.Errorf("%s: exit %d stdout %q", name, res.Code, res.Stdout)
		}
		testkit.Contains(t, name, res.Stderr, "corrupt registry "+registry)
		testkit.Contains(t, name, res.Stderr, ". Nothing runs inside this project until it's fixed.\n")
	}
	// the global store is not behind the project's file
	testkit.Eq(t, "global run", e.RunWith(in(proj), "r", "outside").Stdout, "outside\n")

	// Looking is not running: show reads past the file, quietly
	for _, cmd := range [][]string{{"show", "outside"}, {"show", "--json", "outside"}} {
		res := e.RunWith(in(proj), cmd...)
		if strings.Contains(res.Stderr, "corrupt registry") || strings.Contains(res.Stderr, "warning") {
			t.Errorf("%v: stderr %q", cmd, res.Stderr)
		}
	}
	testkit.Contains(t, "show", e.RunWith(in(proj), "show", "outside").Stdout, "Scope:       global")
	testkit.Contains(t, "show . nothing", e.RunWith(in(proj), "show", ".", "nothing").Stderr, "'nothing' not found")

	// rm is global-only, so a broken project file doesn't matter
	testkit.Eq(t, "rm", e.RunWith(in(proj), "rm", "outside", "-y").Code, 0)
	wantGone(t, e, "outside")
	testkit.Contains(t, "list", e.RunWith(in(proj), "list").Stderr, "corrupt registry")
}

func TestStores_ARepairedProjectRunsAgain(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"build": testkit.Inline("echo global")})
	proj := e.Project(map[string]any{"build": testkit.Inline("echo project")})
	registry := filepath.Join(proj, ".shortcuts")
	good := testkit.ReadFile(t, registry)
	e.WriteFile(registry, "<<<<<<< HEAD\n"+good, 0)
	res := e.RunWith(in(proj), "r", ".", "build")
	res.WantCode(t, 1)
	testkit.Contains(t, "broken", res.Stderr, "Nothing runs inside this project")
	testkit.Eq(t, "global", e.RunWith(in(proj), "r", "build").Stdout, "global\n")
	e.WriteFile(registry, good, 0)
	testkit.Eq(t, "repaired", e.RunWith(in(proj), "r", ".", "build").Stdout, "project\n")
}

func TestStores_BrokenGlobalStoreStillReports(t *testing.T) {
	e := testkit.New(t)
	e.CorruptStore(e.Store)
	res := e.Run("r", "anything")
	res.WantCode(t, 1)
	testkit.Contains(t, "run", res.Stderr, "corrupt registry")
	testkit.Contains(t, "list", e.Run("list").Stderr, "corrupt registry")
}

func TestRunning_CommandWordsAreOrdinaryShortcutNames(t *testing.T) {
	e := testkit.New(t)
	for _, name := range []string{"new", "list", "run", "r", "nano"} {
		runOK(t, e, testkit.Opts{}, "new", name, "--", "echo ran "+name)
		testkit.Eq(t, "run "+name, e.Run("run", name).Stdout, "ran "+name+"\n")
	}
	if !slices.Contains(strings.Fields(e.Run("list", "--names").Stdout), "run") {
		t.Errorf("list --names lacks run")
	}
}

func TestRunning_AQuotedCommandKeepsPositionalArgumentsLive(t *testing.T) {
	e := testkit.New(t)
	for name, command := range map[string]string{"cmd0": `echo "$1"`, "cmd1": `printf '[%s]' "$@"`, "cmd3": `echo '$HOME'`} {
		runOK(t, e, testkit.Opts{}, "new", name, "--", command)
		testkit.Eq(t, name, e.LoadGlobal()[name].(map[string]any)["inline"], command)
	}
	testkit.Eq(t, "cmd0", e.Run("r", "cmd0", "hello world").Stdout, "hello world\n")
	testkit.Eq(t, "cmd1", e.Run("r", "cmd1", "a b", "c").Stdout, "[a b][c]")
	testkit.Eq(t, "cmd3", e.Run("r", "cmd3").Stdout, "$HOME\n")
}

// A NUL in a cwd cannot be a directory: the run is refused and the command does not start.
func TestRunning_ANulInCwdRefusesTheRun(t *testing.T) {
	e := testkit.New(t)
	marker := filepath.Join(e.Tmp, "ran")
	e.WriteStore(e.Store, map[string]any{"nul": map[string]any{"inline": "touch " + marker, "cwd": "a\x00b"}})
	res := e.Run("run", "nul")
	res.WantCode(t, 1)
	if _, err := os.Stat(marker); err == nil {
		t.Error("the command ran")
	}
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
		testkit.Dirs("src"), testkit.Files(map[string]string{"bin/run.sh": bashShebang + "pwd\n"}), testkit.Mode(0o755))
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

func TestStores_UnknownTopLevelFieldStopsAGlobalShortcut(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hi": testkit.Inline("echo ran")}, map[string]any{"future_safety": true})
	res := e.Run("r", "hi")
	testkit.Eq(t, "run", res.Code, 1)
	testkit.Contains(t, "run", res.Stderr, "unknown field: future_safety")
	if strings.Contains(res.Stdout, "ran") {
		t.Error("it ran")
	}
	runOK(t, e, testkit.Opts{}, "rm", "hi", "-y")
}

func TestStores_NamespaceFlagKeepsEachGroupsScriptsApart(t *testing.T) {
	e := testkit.New(t)
	for _, p := range [][2]string{{"docker", "dock"}, {"kube", "kub"}} {
		source := e.WriteFile(filepath.Join(e.Store, "imports", "scripts", p[0], "delete.sh"), bashShebang+"echo "+p[1]+"\n", 0o755)
		runOK(t, e, testkit.Opts{}, "new", "delete", "--script", source, "--namespace", p[0], "--desc", "d")
	}
	testkit.Eq(t, "docker", e.Run("r", "docker", "delete").Stdout, "dock\n")
	testkit.Eq(t, "kube", e.Run("r", "kube", "delete").Stdout, "kub\n")

	cp := filepath.Join(e.Store, "imports", "scripts", "docker", "delete.sh")
	testkit.Eq(t, "script_path", e.Run("show", "--field", "script_path", "docker", "delete").Stdout, cp+"\n")
	runOK(t, e, testkit.Opts{}, "rm", "docker", "delete", "-y")
	if !fileExists(cp) || !fileExists(filepath.Join(e.Store, "imports", "scripts", "kube", "delete.sh")) {
		t.Error("rm deleted a script file")
	}
}

func TestRunning_ExitCodeAndSignalsPassStraightThrough(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{
		"fail": testkit.Inline("echo out; echo err >&2; exit 42"),
		"pid":  testkit.Inline(`echo "$$"; exec sleep 30`),
		"trap": testkit.Inline(`trap 'kill $!; exit 7' USR1; echo ready; sleep 30 >/dev/null 2>&1 & wait`),
	})
	// stdout and stderr stay apart, and the exit code is the shortcut's own
	e.RunWith(testkit.Opts{Env: map[string]string{"SHORTCUT_SHOW_DIR": "0"}}, "r", "fail").Want(t, 42, "out\n", "err\n")

	// The run is the shortcut's process: same pid, and nothing in between to relay a signal
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP, syscall.SIGKILL} {
		p := e.Start(testkit.Opts{}, "run", "pid")
		line, err := p.Stdout().ReadString('\n')
		if err != nil || strings.TrimSpace(line) != strconv.Itoa(p.Pid()) {
			t.Fatalf("%v: first line %q (%v), pid %d", sig, line, err, p.Pid())
		}
		p.Signal(sig)
		if res := p.Wait(5 * time.Second); res.Code != -int(sig) {
			t.Errorf("%v: exit %d", sig, res.Code)
		}
	}

	// A signal the shortcut handles is its to answer
	p := e.Start(testkit.Opts{}, "run", "trap")
	if line, _ := p.Stdout().ReadString('\n'); line != "ready\n" {
		t.Fatalf("first line %q", line)
	}
	p.Signal(syscall.SIGUSR1)
	if res := p.Wait(5 * time.Second); res.Code != 7 {
		t.Errorf("USR1: exit %d", res.Code)
	}
}

// bash pipes the run into head and and reports how the run ended (141 = SIGPIPE).
func TestRunning_AShortcutStartsWithSigpipeOn(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"seq": testkit.Inline("seq 1 1000000")})
	res := e.Shell(testkit.Opts{Timeout: 10 * time.Second}, `shortcut run seq | head -n 1; echo "${PIPESTATUS[0]}" >&2`)
	if !strings.HasPrefix(res.Stdout, "1\n") {
		t.Errorf("head: %q", res.Stdout)
	}
	if !strings.HasSuffix(res.Stderr, "141\n") || strings.Contains(res.Stderr, "Broken pipe") {
		t.Errorf("seq did not die of SIGPIPE: stderr %q", res.Stderr)
	}
}

// The signal dispositions a shortcut starts with: SIGPIPE and SIGXFSZ default, nothing blocked.
func TestRunning_ASignalMaskAndDispositionsAreTheDefaults(t *testing.T) {
	if _, err := os.Stat("/proc/self/status"); err != nil {
		t.Skip("no /proc")
	}
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"sig": testkit.Inline("grep -E '^Sig(Blk|Ign)' /proc/self/status")})
	res := e.RunWith(testkit.Opts{Env: map[string]string{"SHORTCUT_SHOW_DIR": "0"}}, "r", "sig")
	masks := map[string]uint64{}
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		name, hex, _ := strings.Cut(line, ":")
		v, err := strconv.ParseUint(strings.TrimSpace(hex), 16, 64)
		if err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		masks[name] = v
	}
	if len(masks) != 2 {
		t.Fatalf("stdout %q stderr %q", res.Stdout, res.Stderr)
	}
	if masks["SigBlk"] != 0 {
		t.Errorf("SigBlk %x, want 0", masks["SigBlk"])
	}
	for _, sig := range []syscall.Signal{syscall.SIGPIPE, syscall.SIGXFSZ} {
		if masks["SigIgn"]&(1<<(uint(sig)-1)) != 0 {
			t.Errorf("SigIgn %x has %v ignored", masks["SigIgn"], sig)
		}
	}
}

// The caller's cwd is the physical one, whatever PWD says: SHORTCUT_CALLER_CWD, show --json's run_cwd and the
// project a run belongs to.
func TestCwd_ASymlinkedCwdIsThePhysicalOne(t *testing.T) {
	e := testkit.New(t)
	real := e.Folder("real")
	link := filepath.Join(e.Tmp, "link")
	e.Symlink(real, link)
	e.WriteStore(e.Store, map[string]any{"where": testkit.Inline(`echo "$SHORTCUT_CALLER_CWD"; pwd -P`), "c": testkit.Inline("true")})
	res := e.RunWith(testkit.Opts{Cwd: link}, "r", "where")
	if res.Code != 0 || res.Stdout != real+"\n"+real+"\n" || !strings.HasSuffix(res.Stderr, "→ "+real+"\n") {
		t.Errorf("from the link: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	info := decodeObject(t, e.RunWith(testkit.Opts{Cwd: link}, "show", "--json", "c").Stdout)
	testkit.Eq(t, "run_cwd", info["run_cwd"], real)

	proj := e.Project(testkit.GO)
	plink := filepath.Join(e.Tmp, "plink")
	e.Symlink(proj, plink)
	res = e.RunWith(testkit.Opts{Cwd: plink}, "r", ".", "go")
	if res.Code != 0 || res.Stdout != "ran\n" {
		t.Errorf("project through a link: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
}

func TestRunning_Confirm(t *testing.T) {
	e := testkit.New(t)
	work := e.Folder("work")
	// no description, as new writes it without --desc
	e.WriteStore(e.Store, map[string]any{
		"danger": map[string]any{"inline": "echo ran; pwd", "confirm": true},
		"wipe":   map[string]any{"inline": "echo ran", "description": "Wipes the cache", "confirm": true},
		"plain":  map[string]any{"inline": "echo ran", "confirm": true},
		"noted":  map[string]any{"script": "scripts/none.sh", "confirm": true},
		"drop":   map[string]any{"inline": `echo "dropped $*"`, "description": "x", "confirm": true},
		"echoes": testkit.Inline(`echo "$@"`),
	})
	o := in(work)
	// ask runs the shortcut on a terminal, waits for the question and types the answer.
	ask := func(o testkit.Opts, wait, answer string, args ...string) (int, string) {
		t.Helper()
		return e.InTerminal(o, []testkit.Key{{Wait: wait, Send: y(answer + "\n")}}, args...)
	}
	question := func(name string) string { return "run '" + name + "' in " + work + "? [y/N]" }

	res := e.RunWith(o, "r", "danger")
	res.WantCode(t, 1)
	testkit.Eq(t, "no terminal stdout", res.Stdout, "")
	testkit.Contains(t, "no terminal", res.Stderr, "pass -y")
	res = e.RunWith(o, "r", "-y", "danger")
	res.WantCode(t, 0)
	testkit.Contains(t, "-y", res.Stdout, "ran")

	rc, out := ask(o, question("danger"), "y", "r", "danger")
	if rc != 0 || !strings.Contains(out, "ran") {
		t.Errorf("y: exit %d\n%s", rc, out)
	}
	rc, out = ask(o, question("danger"), "n", "r", "danger")
	if rc != 1 || strings.Contains(out, "ran\r\n") {
		t.Errorf("n: exit %d\n%s", rc, out)
	}

	// The description shows above the question, and not with -y
	rc, out = ask(o, question("wipe"), "n", "r", "wipe")
	if rc != 1 {
		t.Errorf("wipe: exit %d", rc)
	}
	testkit.Contains(t, "wipe", out, "Wipes the cache\r\n")
	if strings.Index(out, "Wipes the cache") > strings.Index(out, "run 'wipe'") {
		t.Errorf("the description is not above the question:\n%s", out)
	}
	if got := e.RunWith(o, "r", "-y", "wipe").Stdout; strings.Contains(got, "Wipes the cache") {
		t.Errorf("-y shows the description: %q", got)
	}
	// No description given: nothing above the question, not a placeholder or the command
	for _, name := range []string{"plain", "noted"} {
		rc, out = ask(o, question(name), "n", "r", name)
		if rc != 1 || strings.Contains(out, "echo ran") || strings.Contains(out, "(no description)") {
			t.Errorf("%s: exit %d\n%s", name, rc, out)
		}
	}
	// The description and question go to the terminal even when stdout is a file
	file := filepath.Join(e.Tmp, "out.txt")
	toFile := o
	toFile.StdoutTo = file
	rc, out = ask(toFile, "[y/N]", "y", "r", "wipe")
	if rc != 0 {
		t.Errorf("stdout to a file: exit %d", rc)
	}
	testkit.Contains(t, "stdout to a file", out, "Wipes the cache")
	testkit.Eq(t, "out.txt", testkit.ReadFile(t, file), "ran\n")

	// The question names the arguments
	rc, out = ask(o, "run 'drop' with -f 'my db' in "+work+"? [y/N]", "n", "r", "drop", "-f", "my db")
	if rc != 1 {
		t.Errorf("drop: exit %d\n%s", rc, out)
	}
	testkit.Contains(t, "hint", e.RunWith(o, "r", "drop", "-f", "my db").Stderr, "or pass -y\n")
	testkit.Eq(t, "drop -y", e.RunWith(o, "r", "-y", "drop", "-f", "my db").Stdout, "dropped -f my db\n")

	// After the name, -y belongs to the script
	testkit.Eq(t, "echoes -y", e.Run("r", "echoes", "-y").Stdout, "-y\n")
}

func TestRunning_ARunKeepsTheRealTerminal(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"tty": testkit.Inline(`[ -t 1 ] && echo "stdout:tty" || echo "stdout:pipe"; stty size; echo ready; read -r line; echo "got:$line"`)})
	rc, out := e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "ready", Send: y("hello\r")}}, "run", "tty")
	if rc != 0 {
		t.Errorf("exit %d\n%s", rc, out)
	}
	for _, want := range []string{"stdout:tty", "30 100", "got:hello"} {
		testkit.Contains(t, "terminal", out, want)
	}
	input := "x\n"
	res := e.RunWith(testkit.Opts{Input: &input}, "run", "tty")
	testkit.Contains(t, "pipe", res.Stdout, "stdout:pipe")
	testkit.Contains(t, "pipe", res.Stdout, "got:x")
}

func TestRunning_CtrlCReachesTheShortcut(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"sleep": testkit.Inline("echo started; sleep 10")})
	start := time.Now()
	rc, out := e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "started", Send: y("\x03")}}, "run", "sleep")
	if rc != -int(syscall.SIGINT) {
		t.Errorf("exit %d, want death by SIGINT\n%s", rc, out)
	}
	if time.Since(start) > 8*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

// A --confirm shortcut whose question cannot be shown does not run: stdin a terminal, stderr on /dev/full.
func TestRunning_AConfirmQuestionThatCannotBeShownDoesNotRun(t *testing.T) {
	e := testkit.New(t)
	marker := filepath.Join(e.Tmp, "ran")
	e.WriteStore(e.Store, map[string]any{"danger": map[string]any{"inline": "echo ran >" + marker, "confirm": true}})
	rc, out := devFullQuestion(t, e, "r", "danger")
	if rc != 1 {
		t.Errorf("exit %d\n%s", rc, out)
	}
	testkit.NotExist(t, marker)
}

func TestRunning_TheEnvironmentSaysWhatRunsAndFromWhere(t *testing.T) {
	e := testkit.New(t)
	show := `echo "$SHORTCUT_NAME|$SHORTCUT_NAMESPACE|$SHORTCUT_CALLER_CWD"`
	e.WriteImport(e.Store, "dk", testkit.NS(map[string]any{
		"env": testkit.Inline(show)}))
	e.WriteStore(e.Store, map[string]any{"env": testkit.Inline(show), "cnt": testkit.Inline(`env | grep -c '^SHORTCUT_NAME='; echo "$SHORTCUT_NAME"`)})
	caller := e.Folder("caller")
	outer := map[string]string{"SHORTCUT_NAMESPACE": "outer"}
	res := e.RunWith(testkit.Opts{Cwd: caller, Env: outer}, "r", "dk", "env")
	if want := "dk env|dk|" + caller + "\n"; res.Stdout != want {
		t.Errorf("stdout %q, want %q (%s)", res.Stdout, want, res.Stderr)
	}
	res = e.RunWith(testkit.Opts{Cwd: caller, Env: outer}, "r", "env")
	if want := "env||" + caller + "\n"; res.Stdout != want {
		t.Errorf("stdout %q, want %q (%s)", res.Stdout, want, res.Stderr)
	}
	// A preset SHORTCUT_NAME is replaced, not added to
	res = e.RunWith(testkit.Opts{Env: map[string]string{"SHORTCUT_NAME": "outer"}}, "r", "cnt")
	testkit.Eq(t, "one SHORTCUT_NAME", res.Stdout, "1\ncnt\n")
}

// A script is handed to the kernel as it is: its #! line runs it, a binary runs, a file the kernel refuses says so,
// and one that is not executable, missing or not a program shows the system's error.
func TestRunning_AScriptRunsAsItIs(t *testing.T) {
	e := testkit.New(t)
	scripts := filepath.Join(e.Store, "scripts")
	var bin []byte
	for _, p := range []string{"/usr/bin/true", "/bin/true"} {
		if b, err := os.ReadFile(p); err == nil {
			bin = b
			break
		}
	}
	if bin == nil {
		t.Skip("no true binary")
	}
	e.WriteFile(filepath.Join(scripts, "exec.sh"), bashShebang+"echo exec_shebang\n", 0o755)
	e.WriteFile(filepath.Join(scripts, "plain.sh"), "#!/usr/bin/env sh\necho plain\n", 0o644)
	e.WriteFile(filepath.Join(scripts, "bin"), string(bin), 0o755)
	e.WriteFile(filepath.Join(scripts, "macho"), "\xcf\xfa\xed\xfe"+string(make([]byte, 64)), 0o755)
	e.WriteStore(e.Store, map[string]any{
		"gone":  map[string]any{"script": "scripts/gone.sh"},
		"exec":  map[string]any{"script": "scripts/exec.sh"},
		"plain": map[string]any{"script": "scripts/plain.sh"},
		"bin":   map[string]any{"script": "scripts/bin"},
		"macho": map[string]any{"script": "scripts/macho"},
	})
	if res := e.Run("run", "exec"); res.Code != 0 || res.Stdout != "exec_shebang\n" {
		t.Errorf("exec: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	if res := e.Run("run", "bin"); res.Code != 0 || res.Stdout != "" {
		t.Errorf("bin: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	res := e.Run("run", "plain")
	testkit.Eq(t, "plain exit", res.Code, 1)
	testkit.Eq(t, "plain stdout", res.Stdout, "")
	path := filepath.Join(scripts, "plain.sh")
	testkit.Contains(t, "plain stderr", res.Stderr, "exec "+path+": permission denied")
	// A binary the kernel will not run (another system's format) is not handed to bash.
	res = e.Run("run", "macho")
	testkit.Eq(t, "macho exit", res.Code, 1)
	testkit.Eq(t, "macho stdout", res.Stdout, "")
	testkit.Contains(t, "macho stderr", res.Stderr, "exec "+filepath.Join(scripts, "macho")+": exec format error")
	if strings.Contains(res.Stderr, "bash") || strings.Contains(res.Stderr, "line ") {
		t.Errorf("macho was run by bash: %q", res.Stderr)
	}
	res = e.Run("run", "gone")
	testkit.Eq(t, "gone exit", res.Code, 1)
	testkit.Eq(t, "gone stdout", res.Stdout, "")
	testkit.Contains(t, "gone stderr", res.Stderr, "exec "+filepath.Join(scripts, "gone.sh")+": no such file or directory")
}

func TestRunning_RunHelp(t *testing.T) {
	e := testkit.New(t)
	for _, argv := range [][]string{{"run", "--help"}, {"run", "-h"}, {"r", "--help"}} {
		res := e.Run(argv...)
		res.WantCode(t, 0)
		testkit.Contains(t, strings.Join(argv, " "), res.Stdout, "shortcut run|r [-y] [.] <name> [args...]")
	}
	e.WriteStore(e.Store, map[string]any{"echo_args": testkit.Inline(`echo "args: $@"`)})
	res := e.Run("run", "echo_args", "--help")
	res.WantCode(t, 0)
	testkit.Contains(t, "stdout", res.Stdout, "args: --help")
}

func TestRunning_RunCwdModes(t *testing.T) {
	e := testkit.New(t)
	fixed := e.Folder("fixed")
	proj := e.Project(map[string]any{
		"default": testkit.Inline("pwd"),
		"caller":  map[string]any{"inline": "pwd", "cwd": "caller"},
		"root":    map[string]any{"inline": "pwd", "cwd": "."},
		"sibling": map[string]any{"inline": "pwd", "cwd": "frontend"},
		"fixed":   map[string]any{"inline": "pwd", "cwd": fixed},
		"gone":    map[string]any{"inline": "pwd", "cwd": "nope"},
	}, testkit.Dirs("frontend", "src"))
	here := filepath.Join(proj, "src")
	for _, c := range [][2]string{{"default", here}, {"caller", here}, {"root", proj}, {"sibling", filepath.Join(proj, "frontend")}, {"fixed", fixed}} {
		res := e.RunWith(in(here), "r", ".", c[0])
		if res.Stdout != c[1]+"\n" {
			t.Errorf("%s: stdout %q, want %q", c[0], res.Stdout, c[1]+"\n")
		}
		testkit.Contains(t, c[0], res.Stderr, "→ "+c[1]+"\n")
	}
	res := e.RunWith(in(here), "r", ".", "gone")
	res.WantCode(t, 1)
	testkit.Eq(t, "gone stdout", res.Stdout, "")
	testkit.Contains(t, "gone", res.Stderr, filepath.Join(proj, "nope"))
}

func TestRunning_RunDirectoryLine(t *testing.T) {
	e := testkit.New(t)
	work := e.Folder("work")
	e.WriteStore(e.Store, map[string]any{"pwd": testkit.Inline("pwd")})
	e.RunWith(in(work), "run", "pwd").Want(t, 0, work+"\n", "→ "+work+"\n")
	_, out := e.InTerminal(in(work), nil, "run", "pwd")
	testkit.Contains(t, "on a terminal", out, "\x1b[2m→ "+work+"\x1b[0m")
}

func TestRunning_ARunThatCantStartIsAnError(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, testkit.GO)
	res := e.RunWith(testkit.Opts{Env: map[string]string{"PATH": e.Tmp}}, "r", "go")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "error:")
}

func TestRunning_AHandWrittenInvalidNameDoesntRun(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"a b": testkit.Inline("echo ran")})
	res := e.Run("r", "a b")
	if res.Code != 1 || res.Stdout != "" {
		t.Errorf("exit %d stdout %q", res.Code, res.Stdout)
	}
	testkit.Contains(t, "stderr", res.Stderr, "invalid shortcut name 'a b'")
}

func TestRunning_EveryWordReachesTheShortcutAFirstDoubleDashIncluded(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"words": testkit.Inline(`printf "[%s]" "$@"; echo`), "echo": testkit.Inline("echo name=$0 first=$1 all=$*")})
	testkit.Eq(t, "--", e.Run("r", "words", "--", "-5", "x").Stdout, "[--][-5][x]\n")
	testkit.Eq(t, "flags", e.Run("r", "words", "--force", "", "a b").Stdout, "[--force][][a b]\n")
	res := e.Run("run", "echo", "-v", "--option", "arg1", "arg2")
	res.WantCode(t, 0)
	testkit.Eq(t, "$0 and $*", res.Stdout, "name=echo first=-v all=-v --option arg1 arg2\n")
}

func TestRunning_AClosedPipeStopsQuietly(t *testing.T) {
	e := testkit.New(t)
	// More than a pipe holds, so shortcut is still writing when the reader goes
	shortcuts := map[string]any{}
	for i := 0; i < 2000; i++ {
		shortcuts[fmt.Sprintf("s%d", i)] = map[string]any{"inline": "true", "description": strings.Repeat("x", 60)}
	}
	e.WriteStore(e.Store, shortcuts)
	res := e.Shell(testkit.Opts{}, "set -o pipefail; shortcut list | true")
	if res.Code != 141 || res.Stderr != "" {
		t.Errorf("exit %d stderr %q", res.Code, res.Stderr)
	}
}

// A global shortcut still runs from a folder whose name is not valid UTF-8, and the "-> cwd" line shows it as it is.
func TestRun_RunsFromANonUTF8Folder(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hello": testkit.Inline("true")})
	dir := filepath.Join(e.Tmp, "p\xffroj")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Skipf("this filesystem refuses non-UTF-8 names: %v", err)
	}
	res := e.RunWith(testkit.Opts{Cwd: dir}, "run", "hello")
	res.WantCode(t, 0)
	testkit.Contains(t, "stderr", res.Stderr, "p\xffroj")
}

// run and show reach the global store without the dot and only the project with it, for a name and for a group, and
// each side says where the other has the name. Nothing runs on a miss.
func TestScope_TheDotSelectsTheScope(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"build": testkit.Inline("echo global-build"), "gonly": testkit.Inline("echo gonly")})
	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{"ps": testkit.Inline("echo global-ps")}))
	proj := e.Project(map[string]any{"build": testkit.Inline("echo project-build"), "ponly": testkit.Inline("echo ponly")})
	e.WriteImport(filepath.Join(proj, ".shortcuts"), "terraform", testkit.NS(map[string]any{"plan": testkit.Inline("echo plan")}))
	e.WriteImport(filepath.Join(proj, ".shortcuts"), "docker", testkit.NS(map[string]any{"delete": testkit.Inline("echo project-delete")}))
	o := testkit.Opts{Cwd: proj, Env: map[string]string{"SHORTCUT_SHOW_DIR": "0"}}

	for _, cmd := range []string{"r", "show"} {
		// run prints what the shortcut echoes; show prints the entry, whose inline text is "echo " + that.
		for _, c := range []struct {
			args  []string
			out   string
			scope string // show's Scope line, where the name is a shortcut
		}{
			{[]string{"build"}, "global-build", "global"},
			{[]string{"gonly"}, "gonly", "global"},
			{[]string{"docker", "ps"}, "global-ps", ""},
			{[]string{".", "build"}, "project-build", "project"},
			{[]string{".", "ponly"}, "ponly", "project"},
			{[]string{".", "terraform", "plan"}, "plan", ""},
			{[]string{".", "docker", "delete"}, "project-delete", ""},
		} {
			what := cmd + " " + strings.Join(c.args, " ")
			res := e.RunWith(o, append([]string{cmd}, c.args...)...)
			res.WantCode(t, 0)
			if cmd == "r" {
				testkit.Eq(t, what, res.Stdout, c.out+"\n")
				continue
			}
			testkit.Contains(t, what, res.Stdout, "echo "+c.out)
			if c.scope != "" {
				testkit.Contains(t, what, res.Stdout, "Scope:       "+c.scope+"\n")
			}
		}
		if cmd == "r" {
			testkit.Eq(t, "r -y . build", e.RunWith(o, "r", "-y", ".", "build").Stdout, "project-build\n")
		}

		for _, c := range []struct {
			args []string
			want string
		}{
			{[]string{"ponly"}, "shortcut 'ponly' not found. The project in " + proj + " has it: shortcut " + cmd + " . ponly"},
			{[]string{"terraform", "plan"}, "The project in " + proj + " has it: shortcut " + cmd + " . terraform plan"},
			{[]string{"docker", "delete"}, "'delete' not found in namespace 'docker'. The project in " + proj + " has it: shortcut " + cmd + " . docker delete"},
			{[]string{".", "gonly"}, "shortcut 'gonly' not found. Your global store has it: shortcut " + cmd + " gonly"},
			{[]string{".", "docker", "ps"}, "Your global store has it: shortcut " + cmd + " docker ps"},
		} {
			what := cmd + " " + strings.Join(c.args, " ")
			res := e.RunWith(o, append([]string{cmd}, c.args...)...)
			testkit.Eq(t, what+" exit", res.Code, 1)
			testkit.Eq(t, what+" stdout", res.Stdout, "")
			testkit.Contains(t, what, res.Stderr, c.want)
		}

		// The dot outside every project, and the dot alone
		res := e.RunWith(testkit.Opts{Cwd: e.Tmp}, cmd, ".", "build")
		res.WantCode(t, 1)
		testkit.Eq(t, cmd+" outside stdout", res.Stdout, "")
		testkit.Contains(t, cmd+" outside", res.Stderr, "error: no project here: no .shortcuts or .shortcuts.local in this folder or its parents")
		e.RunWith(o, cmd, ".").WantCode(t, 2)
	}
	// show --field and --json follow the dot as well
	testkit.Eq(t, "show --field", e.RunWith(o, "show", "--field", "scope", ".", "build").Stdout, "project\n")
	testkit.Eq(t, "show --json", decodeObject(t, e.RunWith(o, "show", "--json", ".", "build").Stdout)["scope"], "project")
	// the dot is only the first word after the options: later, it is the shortcut's
	e.WriteStore(e.Store, map[string]any{"echoes": testkit.Inline(`echo "$@"`)})
	testkit.Eq(t, "late dot", e.RunWith(o, "r", "echoes", ".").Stdout, ".\n")
}

// A folder above the cwd that can't be searched is an error for the project scope, and the global scope doesn't care.
func TestRunning_AProjectLookupThatCantTellDoesntTouchTheGlobalScope(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can search any folder")
	}
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"x": map[string]any{"inline": "echo ran", "cwd": e.Home}})
	child := filepath.Join(e.Folder("locked"), "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	// the shell enters the folder first and then locks its parent: the cwd stays, but nothing above it can be searched
	script := fmt.Sprintf(`cd '%s' && chmod 000 .. && { shortcut r . x; echo "project=$?"; shortcut r x; echo "global=$?"; shortcut list; echo "list=$?"; } 2>&1`, child)
	res := e.Shell(testkit.Opts{Cwd: e.Home}, script)
	testkit.Contains(t, "project scope", res.Stdout, "permission denied")
	testkit.Contains(t, "project scope", res.Stdout, "project=1\n")
	testkit.Contains(t, "global scope", res.Stdout, "ran\nglobal=0\n")
	testkit.Contains(t, "list", res.Stdout, "warning: ")
	testkit.Contains(t, "list", res.Stdout, "list=0\n")
}

func TestRunning_AnOptionAfterTheDotSaysToMoveItBefore(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"x": testkit.Inline("echo x")})
	o := testkit.Opts{Cwd: proj}
	res := e.RunWith(o, "r", ".", "-y", "loc")
	testkit.Eq(t, "exit", res.Code, 1) // a wrong name is exit 1, as 'not found' is; exit 2 is the missing name
	testkit.Contains(t, "stderr", res.Stderr, "error: '-y' is not a name: options go before the dot, as in shortcut r -y . loc")
	res = e.RunWith(o, "r", ".", "--yes")
	testkit.Contains(t, "alone", res.Stderr, "as in shortcut r -y .\n")
	// show reads options anywhere, so it needs no such message
	testkit.Eq(t, "show", e.RunWith(o, "show", ".", "x", "--json").Code, 0)
}

func TestSymlinkSafety_ARunWritesNothingInTheProjectOrTheStore(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(testkit.GO)
	store, project := treeOf(t, e.Store), treeOf(t, proj)
	if got := e.RunWith(in(proj), "r", ".", "go").Stdout; got != "ran\n" {
		t.Errorf("run: %q", got)
	}
	if after := treeOf(t, e.Store); !maps.Equal(store, after) {
		t.Errorf("the store changed: %v -> %v", store, after)
	}
	if after := treeOf(t, proj); !maps.Equal(project, after) {
		t.Errorf("the project changed: %v -> %v", project, after)
	}
}
