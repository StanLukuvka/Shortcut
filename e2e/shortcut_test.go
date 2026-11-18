package e2e

import (
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// With fds 0, 1 or 2 closed the Go runtime puts /dev/null in their place, so the commands exit as they would
// with them open, print nothing anywhere visible and don't panic (a Go panic exits 2). What the child sees there is /dev/null.
func TestRunning_ClosedStdFDsDoNotStopACommand(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"go": testkit.Inline("echo ran")})
	for _, fds := range [][]int{{0}, {1}, {2}, {0, 1, 2}} {
		for _, args := range [][]string{{"list"}, {"r", "go"}, {"show", "go"}} {
			if res := e.RunCmd(testkit.Opts{CloseFDs: fds}, append([]string{testkit.Binary(t)}, args...)...); res.Code != 0 {
				t.Errorf("%v closed, %v: exit %d stdout %q stderr %q", fds, args, res.Code, res.Stdout, res.Stderr)
			}
		}
	}
	// stdout closed but stderr kept: nothing reaches stderr either
	res := e.RunCmd(testkit.Opts{CloseFDs: []int{1}}, testkit.Binary(t), "list")
	if res.Stderr != "" {
		t.Errorf("stderr %q", res.Stderr)
	}
}

// A write error other than a closed pipe is an error: stdout on /dev/full makes even --version exit 1.
func TestWriteError_StdoutOnDevFullExits1(t *testing.T) {
	res := devFull(t, testkit.New(t), "--version")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "error: writing stdout:")
}

// What the binary prints before any parser runs.
func TestDispatch_ErrorsBeforeAnyParser(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hello": testkit.Inline("echo hello")}) // a name alone never runs it
	const runMissing = "error: missing shortcut name\nusage: shortcut run|r [-y] [.] <name> [args...]\n       shortcut run|r [-y] [.] <namespace> <name> [args...]\n"
	unknown := func(word string) string { return "error: unknown command '" + word + "'\n" }
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"run"}, runMissing},
		{[]string{"run", "-y"}, runMissing},
		{[]string{"r"}, runMissing},
		{[]string{"--bogus"}, "error: unrecognized option '--bogus'\n"},
		{[]string{"bogus"}, unknown("bogus")},
		{[]string{"-x"}, "error: unrecognized option '-x'\n"},
		{[]string{"bogus", "--help"}, unknown("bogus")},
		{[]string{"nano", "x"}, unknown("nano")},
		{[]string{"activate", "x"}, unknown("activate")},
		{[]string{"version", "x"}, unknown("version")},
		{[]string{"hello"}, unknown("hello")},
		{[]string{"nano", "hello"}, unknown("nano")},
	} {
		e.Run(c.argv...).Want(t, 2, "", c.want)
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
		{[]string{"new", "x"}, "error: give a command after '--', or --script PATH\n" + try("new")},
		{[]string{"list", "ns"}, "error: unexpected argument 'ns'\n" + try("list")},
		{[]string{"show"}, "error: missing name\n" + try("show")},
		{[]string{"rm", "-yh"}, "error: unrecognized option '-yh'\n" + try("rm")},
		{[]string{"rm"}, "error: missing name\n" + try("rm")},
	} {
		e.Run(c.argv...).Want(t, 2, "", c.want)
	}
	// The valid side: rm --namespace needs no name, a name after -- is checked as a name, and new keeps its command part.
	if res := e.Run("rm", "--namespace", "nosuch", "-y"); res.Code == 2 {
		t.Errorf("rm --namespace: usage error %q", res.Stderr)
	}
	if res := e.Run("show", "--", "-rf"); res.Code != 1 || strings.Contains(res.Stderr, "unrecognized") {
		t.Errorf("show -- -rf: exit %d stderr %q", res.Code, res.Stderr)
	}
	runOK(t, e, testkit.Opts{}, "new", "foo", "--desc", "x", "--", "echo -- y")
	if got := runOK(t, e, testkit.Opts{}, "show", "foo", "--json").Stdout; !strings.Contains(got, "echo -- y") {
		t.Errorf("new kept the command part wrong: %s", got)
	}
}
