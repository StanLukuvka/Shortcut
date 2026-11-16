package term

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// The test binary doubles as the program under test for what only a process can show: TERM_HELPER names the job.
func TestMain(m *testing.M) { testkit.HelperMain(m, "TERM_HELPER", helper) }

func helper(job string) {
	s := NewStreams(os.Stdin, os.Stdout, os.Stderr)
	switch job {
	case "closed": // started with fds 0, 1 and 2 closed: the runtime has reopened them, so nothing fails
		code := 0
		for fd := 0; fd < 3; fd++ {
			if _, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0); e != 0 {
				code |= 1 << fd
			}
		}
		s.Out.WriteString("out\n")
		s.Err.WriteString("err\n")
		os.Exit(code)
	case "ask":
		ok, _ := Ask(s, "go on? ")
		s.Out.WriteString(fmt.Sprintf("answer=%v\n", ok))
	case "confirm":
		ok, err := Confirm{Question: "run 'go'?", NoTerminal: "no terminal", Message: "will run \x1b[2Kit"}.Granted(s, false)
		s.Out.WriteString(fmt.Sprintf("granted=%v err=%v\n", ok, err))
	}
}

// capture is a Streams over files in the test's temp dir, and what each one holds.
func capture(t *testing.T, e *testkit.Env, input string) (s *Streams, out, errs func() string) {
	t.Helper()
	open := func(name string) *os.File {
		f, err := os.Create(filepath.Join(e.Tmp, name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	in := e.WriteFile(filepath.Join(e.Tmp, "in"), input, 0)
	inf, err := os.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { inf.Close() })
	read := func(name string) func() string {
		return func() string { b, _ := os.ReadFile(filepath.Join(e.Tmp, name)); return string(b) }
	}
	return NewStreams(inf, open("out"), open("err")), read("out"), read("err")
}

func TestStreams_OutIsSafeAndStyled(t *testing.T) {
	e := testkit.New(t)
	s, out, errs := capture(t, e, "")
	if s.Out.IsTerminal() || s.Err.IsTerminal() {
		t.Error("files are not terminals")
	}
	s.Out.WriteString("a\x1b[2Kb\rc\n")
	s.Out.WriteStyled("\x1b[2mdim\x1b[0m\n") // ours: not made safe
	s.Err.WriteString("e\x1b\n")
	if errs() != "e^[\n" {
		t.Errorf("stderr %q", errs())
	}
	if want := "a^[[2Kb^Mc\n\x1b[2mdim\x1b[0m\n"; out() != want {
		t.Errorf("stdout %q, want %q", out(), want)
	}
}

// A process started with fds 0, 1 and 2 closed finds them open on /dev/null.
func TestStreams_ClosedStdFdsAreReopenedByTheRuntime(t *testing.T) {
	e := testkit.New(t)
	res := e.RunCmd(testkit.Opts{Env: map[string]string{"TERM_HELPER": "closed"}},
		"sh", "-c", `exec "$0" -test.run='^$' <&- >&- 2>&-`, os.Args[0])
	res.WantCode(t, 0) // else the code's bits 0-2 say which of fd 0-2 was found closed, bit 3 that the write failed
}

// Q-3: stdout escapes bytes that are not UTF-8 like stderr, in Write and in WriteStyled.
func TestStreams_NonUTF8IsEscapedOnBothStreams(t *testing.T) {
	e := testkit.New(t)
	s, out, errs := capture(t, e, "")
	const raw, want = "a\x9bb\xed\xa0\x80c\n", "a\\x9bb\\xed\\xa0\\x80c\n"
	s.Out.WriteString(raw)
	s.Out.WriteStyled(raw)
	s.Out.WriteString(raw)
	s.Err.WriteString(raw)
	if out() != want+want+want || errs() != want {
		t.Errorf("stdout %q, stderr %q, want %q", out(), errs(), want)
	}
}
