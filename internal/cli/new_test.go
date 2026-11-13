package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// fileShortcuts is the "shortcuts" object of a store or namespace file, as the test sees it.
func fileShortcuts(t *testing.T, path string) map[string]any {
	t.Helper()
	doc := decodeObject(t, readFile(t, path))
	s, _ := doc["shortcuts"].(map[string]any)
	return s
}

func notExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Errorf("%s exists", path)
	}
}

func isExec(t *testing.T, path string) bool {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()&0o111 != 0
}

func inputOf(text string) *string { return &text }

// py: test_running.py::test_command_words_are_ordinary_shortcut_names
func TestRunning_CommandWordsAreOrdinaryShortcutNames(t *testing.T) {
	e := testkit.New(t)
	for _, name := range []string{"new", "list", "run", "r", "nano"} {
		runOK(t, e, testkit.Opts{}, "new", name, "--", "echo ran "+name)
		eq(t, "run "+name, e.Run("run", name).Stdout, "ran "+name+"\n")
	}
	if !slices.Contains(strings.Fields(e.Run("list", "--names").Stdout), "run") {
		t.Errorf("list --names lacks run")
	}
}

// py: test_running.py::test_new_declares_args
func TestRunning_NewDeclaresArgs(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "greet", "--arg", "who", "--arg", "greeting=hello", "--", `echo "$2 $1"`)
	eq(t, "args", e.LoadGlobal()["greet"].(map[string]any)["args"], []any{
		map[string]any{"name": "who", "required": true}, map[string]any{"name": "greeting", "default": "hello"}})
	eq(t, "run", e.Run("r", "greet", "you").Stdout, "hello you\n")
	eq(t, "run without who", e.Run("r", "greet").Code, 2)
	res := e.Run("new", "bad", "--arg", "no spaces", "--", "true")
	eq(t, "bad exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "'bad' is not a valid entry")
}

// py: test_running.py::test_new_declares_rest_args_and_descs
func TestRunning_NewDeclaresRestArgsAndDescs(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "ex", "--arg", "container", "--arg", "cmd...", "--arg-desc", "container=Which one",
		"--arg-desc", "cmd=What to run", "--", `echo "$SC_ARG_CONTAINER|$SC_ARG_CMD"`)
	eq(t, "args", e.LoadGlobal()["ex"].(map[string]any)["args"], []any{
		map[string]any{"name": "container", "required": true, "desc": "Which one"},
		map[string]any{"name": "cmd", "rest": true, "required": true, "desc": "What to run"}})
	eq(t, "run", e.Run("r", "ex", "web", "ls", "/tmp").Stdout, "web|ls /tmp\n")
	res := e.Run("r", "ex", "web")
	eq(t, "missing exit", res.Code, 2)
	contains(t, "stderr", res.Stderr, "missing <cmd>")
	contains(t, "stderr", res.Stderr, "What to run")

	// NAME...= is optional, with no default; NAME...=x has one
	e.Run("new", "opt", "--arg", "rest...=", "--", `echo "$#"`)
	eq(t, "opt args", e.LoadGlobal()["opt"].(map[string]any)["args"], []any{map[string]any{"name": "rest", "rest": true}})
	eq(t, "opt run", e.Run("r", "opt").Stdout, "0\n")
	e.Run("new", "dflt", "--arg", "rest...=x", "--", `echo "$*"`)
	eq(t, "dflt args", e.LoadGlobal()["dflt"].(map[string]any)["args"],
		[]any{map[string]any{"name": "rest", "rest": true, "default": "x"}})

	// --arg-desc takes the name with or without its ...; a newline shows as \\n
	e.Run("new", "dots", "--arg", "files...", "--arg-desc", "files...=Two\nlines", "--", `echo "$@"`)
	eq(t, "dots desc", e.LoadGlobal()["dots"].(map[string]any)["args"].([]any)[0].(map[string]any)["desc"], "Two\nlines")
	contains(t, "show", e.Run("show", "dots").Stdout, "  files...  required  Two\\nlines\n")

	for _, c := range []struct {
		flags   []string
		message string
	}{
		{[]string{"--arg", "a...", "--arg", "b"}, "only the last entry in 'args' can be 'rest'"},
		{[]string{"--arg", "a", "--arg", "a"}, "arg 'a' is declared twice"},
		{[]string{"--arg", "a", "--arg-desc", "a=one", "--arg-desc", "a=two"}, "--arg-desc a=two: 'a' already has a desc"},
		{[]string{"--arg", "a", "--arg-desc", "b=Nope"}, "--arg-desc b=Nope: needs NAME=TEXT, with NAME declared by --arg"},
		{[]string{"--arg", "a", "--arg-desc", "a"}, "--arg-desc a: needs NAME=TEXT"},
		{[]string{"--arg-desc", "a=No args"}, "--arg-desc a=No args: needs NAME=TEXT"},
	} {
		res := e.Run(append(append([]string{"new", "bad"}, c.flags...), "--", `echo "$@"`)...)
		eq(t, strings.Join(c.flags, " "), res.Code, 1)
		contains(t, "stderr", res.Stderr, c.message)
		if _, has := e.LoadGlobal()["bad"]; has {
			t.Errorf("%v: bad was stored", c.flags)
		}
	}
}

// py: test_running.py::test_new_says_when_a_command_ignores_arguments
func TestRunning_NewSaysWhenACommandIgnoresArguments(t *testing.T) {
	e := testkit.New(t)
	const hint = "won't see any arguments"
	contains(t, "a", e.Run("new", "a", "--", "rg --hidden").Stderr, hint)
	contains(t, "b", e.Run("new", "b", "--", "rg", "--hidden").Stderr, hint)
	for _, c := range [][2]string{{"c", `rg "$@"`}, {"d", "echo $1"}, {"e", "echo ${2}"}, {"f", "echo $*"}, {"g", "echo $SC_ARG_WHO"}} {
		res := runOK(t, e, testkit.Opts{}, "new", c[0], "--", c[1])
		if strings.Contains(res.Stderr, hint) {
			t.Errorf("%s: unexpected hint in %q", c[1], res.Stderr)
		}
	}
}

// py: test_running.py::test_several_word_commands_keep_positional_arguments_live
func TestRunning_SeveralWordCommandsKeepPositionalArgumentsLive(t *testing.T) {
	e := testkit.New(t)
	for i, c := range []struct {
		words  []string
		stored string
	}{
		{[]string{"echo", "$1"}, `echo "$1"`},
		{[]string{"printf", "[%s]", "$@"}, `printf '[%s]' "$@"`},
		{[]string{"echo", "a b", "${1}"}, `echo 'a b' "${1}"`},
		{[]string{"echo", "$HOME"}, `echo '$HOME'`},
		{[]string{"echo", `say "$1"`}, `echo "say \"$1\""`},
		{[]string{"echo", "$(id) `id` \\ $1"}, `echo "\$(id) \` + "`id\\`" + ` \\ $1"`},
	} {
		name := "cmd" + string(rune('0'+i))
		runOK(t, e, testkit.Opts{}, append([]string{"new", name, "--"}, c.words...)...)
		eq(t, name, e.LoadGlobal()[name].(map[string]any)["inline"], c.stored)
	}
	eq(t, "cmd0", e.Run("r", "cmd0", "hello world").Stdout, "hello world\n")
	eq(t, "cmd1", e.Run("r", "cmd1", "a b", "c").Stdout, "[a b][c]")
	eq(t, "cmd3", e.Run("r", "cmd3").Stdout, "$HOME\n")
	eq(t, "cmd5", e.Run("r", "cmd5", "x").Stdout, "$(id) `id` \\ x\n")
}

// py: test_running.py::test_args_that_would_share_a_variable_are_refused
func TestRunning_ArgsThatWouldShareAVariableAreRefused(t *testing.T) {
	e := testkit.New(t)
	res := e.Run("new", "two", "--arg", "a-b", "--arg", "a_b", "--", "true")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "args 'a-b' and 'a_b' would both be SC_ARG_A_B")
}

// py: test_stores.py::test_creation_scope_rules
func TestStores_CreationScopeRules(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(nil, testkit.Name("fresh_proj"), testkit.Dirs("sub"))

	// 1. The global store by default, wherever you are
	runOK(t, e, in(proj), "new", "anywhere", "--", "echo 1")
	if _, has := e.LoadGlobal()["anywhere"]; !has {
		t.Errorf("anywhere is not global")
	}
	notExist(t, filepath.Join(proj, ".shortcuts"))

	// 2. --local creates .shortcuts.local on demand, and what it adds runs straight away
	res := runOK(t, e, in(proj), "new", "local_cmd", "--local", "--", "echo local")
	if strings.Contains(res.Stderr, "needs approving") {
		t.Errorf("stderr: %q", res.Stderr)
	}
	if _, err := os.Stat(filepath.Join(proj, ".shortcuts.local")); err != nil {
		t.Errorf("no .shortcuts.local: %v", err)
	}
	notExist(t, filepath.Join(proj, ".shortcuts"))
	eq(t, "run", e.RunWith(in(proj), "run", "local_cmd").Stdout, "local\n")

	// 3. --cwd is stored against the defining file: absolute globally, relative inside the project
	runOK(t, e, testkit.Opts{}, "new", "fixed", "--cwd", e.Tmp, "--", "pwd")
	eq(t, "fixed cwd", e.LoadGlobal()["fixed"].(map[string]any)["cwd"], e.Tmp)
	runOK(t, e, in(proj), "new", "in_sub", "--local", "--cwd", "sub", "--", "pwd")
	eq(t, "in_sub cwd", fileShortcuts(t, filepath.Join(proj, ".shortcuts.local"))["in_sub"].(map[string]any)["cwd"], "sub")
	e.Run("new", "explicit", "--cwd", "caller", "--", "pwd")
	eq(t, "explicit cwd", e.LoadGlobal()["explicit"].(map[string]any)["cwd"], "caller")

	res = e.Run("new", "nowhere", "--cwd", "/nonexistent/path/xyz", "--", "echo 1")
	if res.Code == 0 {
		t.Errorf("nowhere: exit 0")
	}
	contains(t, "stderr", res.Stderr, "--cwd directory does not exist")

	// 4. Duplicate name error
	res = e.RunWith(in(proj), "new", "local_cmd", "--local", "--", "echo duplicate")
	if res.Code == 0 {
		t.Errorf("duplicate: exit 0")
	}
	contains(t, "stderr", res.Stderr, "already exists in")
}

// py: test_stores.py::test_new_in_home_refuses_local_store
func TestStores_NewInHomeRefusesLocalStore(t *testing.T) {
	e := testkit.New(t)
	res := e.RunWith(in(e.Home), "new", "hello", "--local", "--", "echo hi")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "holds the global store")
	if _, err := os.Stat(filepath.Join(e.Store, "shortcuts.json")); err == nil {
		if _, has := e.LoadGlobal()["hello"]; has {
			t.Errorf("hello was stored")
		}
	}
}

// py: test_stores.py::test_new_takes_one_source
func TestStores_NewTakesOneSource(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{})
	source := e.Script("tool.sh", "#!/usr/bin/env bash\n")
	for _, c := range []struct {
		args    []string
		message string
	}{
		{[]string{"new", "x", "--script", source, "--", "true"}, "use one of"},
		{[]string{"new", "x", "--script", source, "--copy", source}, "not allowed with"},
		{[]string{"new", "x", "--local", "--copy", source}, "Use --script"},
		{[]string{"new", "x", "--script", "", "--", "true"}, "need a file name"},
		{[]string{"new", "x", "--copy", ""}, "need a file name"},
		{[]string{"new", "x", "--script", ""}, "need a file name"},
	} {
		res := e.RunWith(in(proj), c.args...)
		if res.Code == 0 {
			t.Errorf("%v: exit 0", c.args)
		}
		contains(t, strings.Join(c.args, " "), res.Stderr, c.message)
	}
	notExist(t, filepath.Join(proj, ".shortcuts.local"))
	notExist(t, filepath.Join(e.Store, "shortcuts.json"))
}

// py: test_stores.py::test_add_and_link_paths
func TestStores_AddAndLinkPaths(t *testing.T) {
	e := testkit.New(t)
	// 1. Add copy from file
	src := e.Script("source.sh", "#!/usr/bin/env bash\necho source\n")
	os.Chmod(src, 0o644)
	runOK(t, e, testkit.Opts{}, "new", "copied", "--copy", src, "--desc", "Copied script")
	copied := filepath.Join(e.Store, "scripts", "copied.sh")
	if !isExec(t, copied) {
		t.Errorf("the copy is not executable")
	}
	if st, err := os.Stat(src); err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("the original changed: %v %v", st, err)
	}

	// 2. Add from stdin
	runOK(t, e, testkit.Opts{Input: inputOf("#!/usr/bin/env bash\necho piped\n")}, "new", "piped", "--copy", "-", "--desc", "Piped script")
	eq(t, "piped", readFile(t, filepath.Join(e.Store, "scripts", "piped.sh")), "#!/usr/bin/env bash\necho piped\n")

	// 3. Link absolute external file
	ext := e.Script("external.sh", "#!/usr/bin/env bash\necho external\n")
	runOK(t, e, testkit.Opts{}, "new", "linked", "--script", ext, "--desc", "Linked script")
	notExist(t, filepath.Join(e.Store, "scripts", "linked.sh"))
	eq(t, "linked", fileShortcuts(t, filepath.Join(e.Store, "shortcuts.json"))["linked"].(map[string]any)["script"], realpath(t, ext))

	// 4. Path containment check
	if _, err := model.ResolveScriptPath(e.Store, "../../../etc/passwd"); err == nil || !strings.Contains(err.Error(), "path traversal detected") {
		t.Errorf("traversal: %v", err)
	}
}

// py: test_stores.py::test_new_oneliner_and_template
func TestStores_NewOneLinerAndTemplate(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "hello", "--", "echo", "hello", "world")
	hello := e.LoadGlobal()["hello"].(map[string]any)
	eq(t, "inline", hello["inline"], "echo hello world")
	eq(t, "description", hello["description"], "echo hello world")

	runOK(t, e, testkit.Opts{}, "new", "template")
	script := filepath.Join(e.Store, "scripts", "template.sh")
	if !isExec(t, script) {
		t.Errorf("the template is not executable")
	}
	eq(t, "content", readFile(t, script), "#!/usr/bin/env bash\nset -euo pipefail\n\n")
	eq(t, "description", e.LoadGlobal()["template"].(map[string]any)["description"], "(no description)")
}

// py: test_stores.py::test_new_opens_no_editor_and_prints_the_script_path
func TestStores_NewOpensNoEditorAndPrintsTheScriptPath(t *testing.T) {
	e := testkit.New(t)
	rc, out := e.InTerminal(testkit.Opts{Env: map[string]string{"VISUAL": "false", "EDITOR": "false"}}, nil, "new", "tmpl", "--desc", "x")
	script := filepath.Join(e.Store, "scripts", "tmpl.sh")
	if rc != 0 {
		t.Fatalf("exit %d\n%s", rc, out)
	}
	contains(t, "output", out, "Edit it at: "+script)
	eq(t, "content", readFile(t, script), "#!/usr/bin/env bash\nset -euo pipefail\n\n")
	if _, has := e.LoadGlobal()["tmpl"]; !has {
		t.Errorf("tmpl was not stored")
	}
}

// py: test_stores.py::test_new_script_inside_the_store_is_kept_relative
func TestStores_NewScriptInsideTheStoreIsKeptRelative(t *testing.T) {
	e := testkit.New(t)
	imports := filepath.Join(e.Store, "imports")
	inside := e.WriteFile(filepath.Join(imports, "dk", "ps.sh"), "#!/usr/bin/env bash\necho ps\n", 0o755)
	copies := e.WriteFile(filepath.Join(imports, "scripts", "dk", "kept.sh"), "#!/usr/bin/env bash\n", 0o755)
	outside := e.Script("out.sh", "#!/usr/bin/env bash\necho out\n")
	mine := e.WriteFile(filepath.Join(e.Store, "tools", "mine.sh"), "#!/usr/bin/env bash\necho mine\n", 0o755)
	for _, argv := range [][]string{
		{"--namespace", "dk", "ps", "--script", inside},
		{"--namespace", "dk", "out", "--script", outside},
		{"--namespace", "dk", "kept", "--script", copies},
		{"mine", "--script", mine},
	} {
		runOK(t, e, testkit.Opts{}, append([]string{"new"}, argv...)...)
	}
	dk := fileShortcuts(t, filepath.Join(imports, "dk.shortcut"))
	eq(t, "ps", dk["ps"].(map[string]any)["script"], "dk/ps.sh")
	eq(t, "kept", dk["kept"].(map[string]any)["script"], realpath(t, copies))
	eq(t, "out", dk["out"].(map[string]any)["script"], realpath(t, outside))
	eq(t, "mine", e.LoadGlobal()["mine"].(map[string]any)["script"], "tools/mine.sh")
	eq(t, "run mine", e.Run("r", "mine").Stdout, "mine\n")
	e.Run("update", "-y", "dk")
	eq(t, "run dk ps", e.Run("r", "dk", "ps").Stdout, "ps\n")
}

// py: test_stores.py::test_namespace_flag_creates_a_namespace
func TestStores_NamespaceFlagCreatesANamespace(t *testing.T) {
	e := testkit.New(t)
	res := runOK(t, e, testkit.Opts{}, "new", "delete", "--namespace", "docker", "--desc", "Remove one", "--", `echo "rm $1"`)
	contains(t, "stderr", res.Stderr, "needs approving before it runs: shortcut update docker")
	doc := decodeObject(t, readFile(t, filepath.Join(e.Store, "imports", "docker.shortcut")))
	eq(t, "version", doc["version"], float64(2))
	eq(t, "inline", doc["shortcuts"].(map[string]any)["delete"].(map[string]any)["inline"], `echo "rm $1"`)
	if _, has := e.LoadGlobal()["delete"]; has {
		t.Errorf("delete is global")
	}

	contains(t, "run", e.Run("r", "docker", "delete", "x").Stderr, "isn't approved yet")
	eq(t, "update", e.Run("update", "docker", "-y").Code, 0)
	eq(t, "run approved", e.Run("r", "docker", "delete", "x").Stdout, "rm x\n")

	eq(t, "second", e.Run("new", "ps", "--namespace", "docker", "--", "echo ps").Code, 0)
	res = e.Run("new", "ps", "--namespace", "docker", "--", "echo again")
	eq(t, "again exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "already exists")
}

// py: test_stores.py::test_namespace_flag_collisions
func TestStores_NamespaceFlagCollisions(t *testing.T) {
	e := testkit.New(t)
	e.Run("new", "docker", "--", "echo plain")
	res := e.Run("new", "ps", "--namespace", "docker", "--", "echo ps")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "'docker' is a shortcut")
	notExist(t, filepath.Join(e.Store, "imports"))

	res = e.Run("new", "ps", "--namespace", "not valid", "--", "echo ps")
	eq(t, "invalid exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "invalid shortcut name")

	e.Run("new", "ps", "--namespace", "kube", "--", "echo ps")
	res = e.Run("new", "kube", "--", "echo plain")
	eq(t, "kube exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "'kube' is a namespace")
}

// py: test_stores.py::test_namespace_flag_is_refused_with_local
func TestStores_NamespaceFlagIsRefusedWithLocal(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{})
	res := e.RunWith(in(proj), "new", "where", "--namespace", "dev", "--local", "--", "pwd")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "--namespace can't be used with --local")
	notExist(t, filepath.Join(proj, ".shortcuts.local"))
}

// py: test_stores.py::test_new_in_a_namespace_gives_its_script_path
func TestStores_NewInANamespaceGivesItsScriptPath(t *testing.T) {
	e := testkit.New(t)
	res := e.Run("new", "ps", "--namespace", "docker", "--desc", "d")
	contains(t, "stdout", res.Stdout, "Edit it at: "+filepath.Join(e.Store, "imports", "scripts", "docker", "ps.sh"))
}

// py: test_stores.py::test_concurrent_new_keeps_every_entry
func TestStores_ConcurrentNewKeepsEveryEntry(t *testing.T) {
	e := testkit.New(t)
	var procs []*testkit.Proc
	var want []string
	for i := range 16 {
		name := "n" + string(rune('a'+i))
		want = append(want, name)
		procs = append(procs, e.Start(testkit.Opts{}, "new", name, "--", "echo "+name))
	}
	for _, p := range procs {
		if res := p.Wait(30 * time.Second); res.Code != 0 {
			t.Errorf("exit %d: %s", res.Code, res.Stderr)
		}
	}
	var got []string
	for name := range e.LoadGlobal() {
		got = append(got, name)
	}
	slices.Sort(got)
	eq(t, "entries", got, want)
}

// py: test_stores.py::test_a_symlinked_store_file_is_written_where_it_leads
func TestStores_ASymlinkedStoreFileIsWrittenWhereItLeads(t *testing.T) {
	e := testkit.New(t)
	dotfiles := e.Folder("dotfiles")
	target := e.WriteFile(filepath.Join(dotfiles, "shortcuts.json"), `{"version": 2, "shortcuts": {}}`, 0o644)
	link := filepath.Join(e.Store, "shortcuts.json")
	e.Symlink(target, link)
	runOK(t, e, testkit.Opts{}, "new", "hi", "--", "echo hi")
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link is gone: %v", err)
	}
	if _, has := fileShortcuts(t, target)["hi"]; !has {
		t.Errorf("hi is not in the target")
	}
	if st, err := os.Stat(target); err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("mode: %v %v", st, err)
	}
	entries, _ := os.ReadDir(dotfiles)
	if len(entries) != 1 || entries[0].Name() != "shortcuts.json" {
		t.Errorf("dotfiles holds %v", entries)
	}
}

// py: test_stores.py::test_unknown_fields_are_errors
func TestStores_UnknownFieldsAreErrors(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, nil) // init --global: an empty store
	for _, c := range []struct {
		name, message string
		data          map[string]any
	}{
		{"top", "unknown field: owner", map[string]any{"version": 2, "owner": "x", "shortcuts": map[string]any{}}},
		{"entry", "unknown field: sandbox", map[string]any{"version": 2, "shortcuts": map[string]any{
			"a": map[string]any{"inline": "true", "sandbox": true}}}},
		{"arg", "unknown args field: secret", map[string]any{"version": 2, "shortcuts": map[string]any{
			"a": map[string]any{"inline": "true", "args": []any{map[string]any{"name": "x", "secret": true}}}}}},
	} {
		e.WriteImport(e.Store, c.name, c.data)
		res := e.Run("update", c.name, "-y")
		eq(t, c.name, res.Code, 1)
		contains(t, c.name, res.Stderr, c.message)
		contains(t, c.name+" list", e.Run("list").Stderr, c.message)
	}
}

// py: test_stores.py::test_update_refuses_entries_that_cannot_run
func TestStores_UpdateRefusesEntriesThatCannotRun(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, nil) // init --global: an empty store
	for _, c := range []struct {
		name, shortcut, message string
		entry                   map[string]any
	}{
		{"neither", "a", "exactly one of 'inline' and 'script'", map[string]any{"description": "x"}},
		{"both", "a", "exactly one of 'inline' and 'script'", map[string]any{"inline": "true", "script": "a.sh"}},
		{"missing", "a", "script not found: scripts/a.sh", map[string]any{"script": "scripts/a.sh"}},
		{"escape", "a", "path traversal", map[string]any{"script": "../../outside.sh"}},
		{"badname", "a!", "invalid shortcut name", map[string]any{"inline": "true"}},
	} {
		e.WriteImport(e.Store, c.name, map[string]any{"version": 2, "shortcuts": map[string]any{c.shortcut: c.entry}})
		res := e.Run("update", c.name, "-y")
		eq(t, c.name, res.Code, 1)
		contains(t, c.name, res.Stderr, c.message)
		refused := e.Run("r", c.name, c.shortcut)
		eq(t, c.name+" run", refused.Code, 1)
		want := "isn't approved yet"
		if strings.Contains(c.message, "exactly one") {
			want = c.message
		}
		contains(t, c.name+" run", refused.Stderr, want)
	}
}

// py: test_stores.py::test_default_home_store_is_never_a_project
func TestStores_DefaultHomeStoreIsNeverAProject(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, nil) // init --global, with the default store: ~/.shortcuts
	env := map[string]string{"SHORTCUTS_HOME": filepath.Join(e.Tmp, "custom_store")}
	work := filepath.Join(e.Home, "work")
	os.Mkdir(work, 0o755)
	res := e.RunWith(testkit.Opts{Cwd: e.Home, Env: env}, "new", "here", "--local", "--", "echo 1")
	eq(t, "home exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "holds the global store")
	eq(t, "global", e.LoadGlobal(), map[string]any{})
	runOK(t, e, testkit.Opts{Cwd: work, Env: env}, "new", "here", "--local", "--", "echo 1")
	if _, err := os.Stat(filepath.Join(work, ".shortcuts.local")); err != nil {
		t.Errorf("no .shortcuts.local: %v", err)
	}
	eq(t, "global", e.LoadGlobal(), map[string]any{})
}

// py: test_stores.py::test_local_only_uses_a_store_in_this_directory
func TestStores_LocalOnlyUsesAStoreInThisDirectory(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"up": inline("true")}, testkit.Dirs("sub"))
	registry := filepath.Join(proj, ".shortcuts")
	before := readFile(t, registry)
	source := e.Script("tool.sh", "#!/usr/bin/env bash\n")
	os.Chmod(source, 0o644)
	sub := filepath.Join(proj, "sub")
	for _, args := range [][]string{
		{"new", "x", "--local", "--", "true"},
		{"new", "x", "--script", source, "--desc", "d", "--local"},
	} {
		res := e.RunWith(in(sub), args...)
		eq(t, strings.Join(args, " "), res.Code, 1)
		contains(t, "stderr", res.Stderr, "is inside the project at")
	}
	notExist(t, filepath.Join(sub, ".shortcuts.local"))
	eq(t, "registry", readFile(t, registry), before)
}

// py: test_stores.py::test_newer_store_is_refused_not_rewritten
func TestStores_NewerStoreIsRefusedNotRewritten(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hi": inline("echo hi")}, map[string]any{"version": 3})
	file := filepath.Join(e.Store, "shortcuts.json")
	before := readFile(t, file)
	for _, args := range [][]string{{"r", "hi"}, {"new", "more", "--", "echo more"}} {
		res := e.Run(args...)
		eq(t, strings.Join(args, " "), res.Code, 1)
		contains(t, "stderr", res.Stderr, "upgrade shortcut")
	}
	eq(t, "file", readFile(t, file), before)
}

// A namespace's scripts folder may be a symlink a repo committed: nothing is made or written where it leads.
func TestNew_RefusesAScriptsFolderThatIsASymlink(t *testing.T) {
	e := testkit.New(t)
	elsewhere := e.Folder("elsewhere")
	e.Symlink(elsewhere, filepath.Join(e.Store, "imports", "scripts"))
	res := e.Run("new", "ps", "--namespace", "dk", "--desc", "d")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "is a symlink, so this would write somewhere else")
	entries, _ := os.ReadDir(elsewhere)
	eq(t, "elsewhere", len(entries), 0)
	notExist(t, filepath.Join(e.Store, "imports", "dk.shortcut"))
}
