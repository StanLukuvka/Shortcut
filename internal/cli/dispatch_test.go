package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// The dispatch cases of the golden file: what the binary prints before any parser runs.
func TestDispatch_GoldenErrors(t *testing.T) {
	e := testkit.New(t)
	n := 0
	for _, c := range loadGolden(t, "argparse_errors.json") {
		if c.Command != "(dispatch)" {
			continue
		}
		n++
		res := e.Run(c.Argv...)
		if res.Code != *c.Exit || res.Stderr != c.Stderr || res.Stdout != "" {
			t.Errorf("%v: exit %d stdout %q stderr %q; want exit %d stderr %q", c.Argv, res.Code, res.Stdout, res.Stderr, *c.Exit, c.Stderr)
		}
	}
	if n != 7 {
		t.Errorf("replayed %d dispatch cases, the file has 7", n)
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
	// -yh: a combined short flag reaches -h, wherever it sits.
	if res := e.Run("update", "-yh"); res.Code != 0 || res.Stdout != helpText("update") {
		t.Errorf("update -yh: exit %d stdout %q", res.Code, res.Stdout)
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
		{[]string{"list", "-", "\xff"}, 2, "usage: shortcut list [-h] [--names [NAMESPACE]]\nshortcut list: error: unrecognized arguments: - \\udcff\n"},
		{[]string{"show", "hi", "\xff"}, 1, "error: too many names: hi '\\udcff'\n"},
	} {
		res := e.Run(c.args...)
		if res.Code != c.code || res.Stdout != "" || res.Stderr != c.want {
			t.Errorf("%q: exit %d stdout %q stderr %q, want exit %d stderr %q", c.args, res.Code, res.Stdout, res.Stderr, c.code, c.want)
		}
	}
}
