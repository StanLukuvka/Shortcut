package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// What the binary prints before any parser runs.
func TestDispatch_ErrorsBeforeAnyParser(t *testing.T) {
	e := testkit.New(t)
	const runMissing = "error: missing shortcut name\nusage: shortcut run|r [--global] [-y] <name> [args...]\n       shortcut run|r [--global] [-y] <namespace> <name> [args...]\n"
	const unknown = "error: unknown command 'bogus' (to run a shortcut: shortcut run bogus)\n"
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"run"}, runMissing},
		{[]string{"run", "-y"}, runMissing},
		{[]string{"r"}, runMissing},
		{[]string{"--bogus"}, "error: unrecognized option '--bogus'\nTry 'shortcut --help' for more information.\n"},
		{[]string{"bogus"}, unknown},
		{[]string{"-x"}, "error: unrecognized option '-x'\nTry 'shortcut --help' for more information.\n"},
		{[]string{"bogus", "--help"}, unknown},
	} {
		res := e.Run(c.argv...)
		if res.Code != 2 || res.Stderr != c.want || res.Stdout != "" {
			t.Errorf("%v: exit %d stdout %q stderr %q; want exit 2 stderr %q", c.argv, res.Code, res.Stdout, res.Stderr, c.want)
		}
	}
}

// Each command with one valid and one invalid invocation, end to end.
func TestDispatch_ParserErrorsEndToEnd(t *testing.T) {
	e := testkit.New(t)
	try := func(cmd string) string { return "Try 'shortcut " + cmd + " -h' for more information.\n" }
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"new", "x", "--des", "d", "--", "true"}, "error: unrecognized option '--des'\n" + try("new")},
		{[]string{"new", "x", "--script", "a", "--copy", "b"}, "error: --script and --copy cannot be used together\n" + try("new")},
		{[]string{"list", "ns"}, "error: unexpected argument 'ns'\n" + try("list")},
		{[]string{"show"}, "error: missing name\n" + try("show")},
		{[]string{"location", "-1"}, "error: unrecognized option '-1'\n" + try("location")},
		{[]string{"rm", "-yh"}, "error: unrecognized option '-yh'\n" + try("rm")},
		{[]string{"rm"}, "error: missing name\n" + try("rm")},
		{[]string{"promote", "a", "--as"}, "error: option '--as' needs a value\n" + try("promote")},
		{[]string{"update", "a", "b"}, "error: unexpected argument 'b'\n" + try("update")},
		{[]string{"init", "--global=1"}, "error: option '--global' takes no value\n" + try("init")},
		{[]string{"completion", "sh"}, "error: invalid choice 'sh' (choose from bash, fish, zsh)\n" + try("completion")},
	} {
		res := e.Run(c.argv...)
		if res.Code != 2 || res.Stdout != "" || res.Stderr != c.want {
			t.Errorf("%v: exit %d stdout %q stderr %q; want exit 2 stderr %q", c.argv, res.Code, res.Stdout, res.Stderr, c.want)
		}
	}
	// The valid side: rm --namespace needs no name, a name after -- is checked as a name, and new keeps its command part.
	if res := e.Run("rm", "--namespace", "nosuch", "-y"); res.Code == 2 {
		t.Errorf("rm --namespace: usage error %q", res.Stderr)
	}
	if res := e.Run("show", "--", "-rf"); res.Code != 1 || strings.Contains(res.Stderr, "unrecognized") {
		t.Errorf("show -- -rf: exit %d stderr %q", res.Code, res.Stderr)
	}
	runOK(t, e, testkit.Opts{}, "new", "foo", "--desc", "x", "--", "echo", "--", "y")
	if got := runOK(t, e, testkit.Opts{}, "show", "foo", "--json").Stdout; !strings.Contains(got, "echo -- y") {
		t.Errorf("new kept the command part wrong: %s", got)
	}
}

func TestRunning_UnknownCommandsPointToRun(t *testing.T) {
	e := testkit.New(t)
	for _, word := range []string{"nano", "activate", "version"} {
		res := e.Run(word, "x")
		if res.Code != 2 {
			t.Errorf("%s: exit %d", word, res.Code)
		}
		if want := "error: unknown command '" + word + "' (to run a shortcut: shortcut run " + word + ")\n"; res.Stderr != want {
			t.Errorf("%s: stderr %q, want %q", word, res.Stderr, want)
		}
	}
	res := e.Run("--bogus")
	if res.Code != 2 || !strings.HasPrefix(res.Stderr, "error: unrecognized option '--bogus'\n") {
		t.Errorf("--bogus: exit %d stderr %q", res.Code, res.Stderr)
	}
	if code := e.Run("init", "-g").Code; code != 2 {
		t.Errorf("init -g: exit %d", code)
	}
}

func TestRunning_RunWithoutANameIsAnError(t *testing.T) {
	e := testkit.New(t)
	res := e.Run("run")
	if res.Code != 2 || !strings.HasPrefix(res.Stderr, "error: missing shortcut name\nusage: shortcut run") {
		t.Errorf("exit %d stderr %q", res.Code, res.Stderr)
	}
}

func TestRunning_HelpNamesACommand(t *testing.T) {
	e := testkit.New(t)
	res := e.Run("help", "new")
	if res.Code != 0 || !strings.Contains(res.Stdout, "usage: shortcut new") {
		t.Errorf("help new: exit %d stdout %q", res.Code, res.Stdout)
	}
	if out := e.Run("help", "run").Stdout; !strings.Contains(out, "usage: shortcut run") {
		t.Errorf("help run: %q", out)
	}
}

// Every command's `help <cmd>` and `<cmd> -h` print its file in help/ (run and r print RUN_USAGE), and nothing else.
func TestHelp_LiveOutputIsTheFile(t *testing.T) {
	e := testkit.New(t)
	for _, cmd := range []string{"run", "new", "list", "show", "location", "rm", "promote", "update", "init", "completion"} {
		file, err := os.ReadFile("help/" + cmd + ".txt")
		if cmd == "run" {
			file, err = []byte(runUsage+"\n"), nil
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, argv := range [][]string{{"help", cmd}, {cmd, "-h"}, {cmd, "--help"}} {
			res := e.Run(argv...)
			if res.Code != 0 || res.Stdout != string(file) || res.Stderr != "" {
				t.Errorf("%v: exit %d stderr %q stdout differs from help/%s.txt", argv, res.Code, res.Stderr, cmd)
			}
		}
	}
	for _, argv := range [][]string{{"help", "r"}, {"r", "-h"}, {"r", "--help"}} {
		if res := e.Run(argv...); res.Code != 0 || res.Stdout != runUsage+"\n" {
			t.Errorf("%v: exit %d stdout %q", argv, res.Code, res.Stdout)
		}
	}
}

// The words that show the whole usage, the version, and the commands that are still to be ported.
func TestDispatch_UsageVersionAndStubs(t *testing.T) {
	e := testkit.New(t)
	for _, argv := range [][]string{{}, {"-h"}, {"--help"}, {"help"}, {"help", "nosuch"}} {
		res := e.Run(argv...)
		if res.Code != 0 || res.Stdout != usageText || res.Stderr != "" {
			t.Errorf("%v: exit %d stderr %q", argv, res.Code, res.Stderr)
		}
	}
	if !strings.HasPrefix(usageText, "shortcut - run and manage quick scripts and commands\n\nUsage:\n") || !strings.HasSuffix(usageText, "line.\n") {
		t.Errorf("usageText is not print_usage's text: %q", usageText)
	}
	if res := e.Run("--version"); res.Code != 0 || res.Stdout != "shortcut 0.1.0-dev\n" || Version != "0.1.0-dev" {
		t.Errorf("--version: exit %d stdout %q", res.Code, res.Stdout)
	}
	// An empty word is an unknown command, not a crash.
	if res := e.Run(""); res.Code != 2 || !strings.HasPrefix(res.Stderr, "error: unknown command ''") {
		t.Errorf("empty word: exit %d stderr %q", res.Code, res.Stderr)
	}
}

// The raw bytes of control characters in an unknown word are made safe, as in Python (stderr is a SafeOutput).
func TestDispatch_ErrorsAreSafe(t *testing.T) {
	e := testkit.New(t)
	res := e.Run("bad\x1b[31mword")
	if res.Code != 2 || strings.Contains(res.Stderr, "\x1b") || !strings.Contains(res.Stderr, "bad^[[31mword") {
		t.Errorf("exit %d stderr %q", res.Code, res.Stderr)
	}
}

// Python decodes argv with surrogateescape and stderr prints with backslashreplace; expected text is from Python.
func TestStderr_NonUTF8ArgvPrintsAsEscapes(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "hi", "--", "echo hi")
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"show", "\xff"}, 1, "error: shortcut '\\udcff' not found.\n"},
		{[]string{"--j\xff"}, 2, "error: unrecognized option '--j\\udcff'\nTry 'shortcut --help' for more information.\n"},
		{[]string{"list", "-", "\xff"}, 2, "error: unexpected argument '-'\nTry 'shortcut list -h' for more information.\n"},
		{[]string{"list", "\xff"}, 2, "error: unexpected argument '\\udcff'\nTry 'shortcut list -h' for more information.\n"},
		{[]string{"show", "hi", "\xff"}, 1, "error: too many names: hi '\\udcff'\n"},
	} {
		res := e.Run(c.args...)
		if res.Code != c.code || res.Stdout != "" || res.Stderr != c.want {
			t.Errorf("%q: exit %d stdout %q stderr %q, want exit %d stderr %q", c.args, res.Code, res.Stdout, res.Stderr, c.code, c.want)
		}
	}
}
