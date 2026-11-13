package textio

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// The test binary doubles as the program under test for what only a process can show: TEXTIO_HELPER names the job.
func TestMain(m *testing.M) {
	if job := os.Getenv("TEXTIO_HELPER"); job != "" {
		helper(job)
		os.Exit(0)
	}
	testkit.Main(m)
}

func helper(job string) {
	s := NewStreams(os.Stdin, os.Stdout, os.Stderr)
	switch job {
	case "closed": // started with fds 0, 1 and 2 closed: the runtime has reopened them, so nothing fails (C22)
		code := 0
		for fd := 0; fd < 3; fd++ {
			if _, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0); e != 0 {
				code |= 1 << fd
			}
		}
		s.Out.WriteString("out\n")
		s.Err.WriteString("err\n")
		if s.Flush() != nil {
			code |= 8
		}
		os.Exit(code)
	case "ask":
		fmt.Fprintf(s.Out, "answer=%v\n", Ask(s, "go on? ", nil))
	case "confirm":
		ok, err := Confirm{Question: "run 'go'?", NoTerminal: "no terminal", Message: "will run \x1b[2Kit"}.Granted(s, false)
		fmt.Fprintf(s.Out, "granted=%v err=%v\n", ok, err)
	case "diffpipe": // stdout is a pipe whose reader has gone: ShowDiff's own flush must end the run with 141
		signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE) // as cli.Main does: EPIPE comes back as an error
		time.Sleep(300 * time.Millisecond)
		ShowDiff(s, []string{"--- a/f", "+++ b/f", "+x"})
		os.Exit(0) // no flush of ours: reaching here means ShowDiff swallowed the error
	case "diff":
		var lines []string
		for i := 0; i < atoi(os.Getenv("TEXTIO_LINES")); i++ {
			lines = append(lines, []string{"--- a/f", "+++ b/f", "@@ -1 +1 @@", "+added \x1b[2J", "-removed", " context"}[i%6])
		}
		ShowDiff(s, lines)
		s.Flush()
	}
}

func atoi(s string) (n int) { fmt.Sscan(s, &n); return }

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

func TestStreams_BufferedOutSafeAndStyled(t *testing.T) {
	e := testkit.New(t)
	s, out, errs := capture(t, e, "")
	if s.Out.IsTerminal() || s.Err.IsTerminal() {
		t.Error("files are not terminals")
	}
	fmt.Fprint(s.Out, "a\x1b[2Kb\rc\n")
	if out() != "" {
		t.Error("stdout is buffered when it is not a terminal")
	}
	s.Out.WriteStyled("\x1b[2mdim\x1b[0m\n") // ours: not made safe
	s.Err.WriteString("e\x1b\n")             // stderr is written straight away
	if errs() != "e^[\n" {
		t.Errorf("stderr %q", errs())
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if want := "a^[[2Kb^Mc\n\x1b[2mdim\x1b[0m\n"; out() != want {
		t.Errorf("stdout %q, want %q", out(), want)
	}
	s.Out.WriteString(strings.Repeat("x", bufSize-1))
	if len(out()) != len("a^[[2Kb^Mc\n\x1b[2mdim\x1b[0m\n") {
		t.Error("flushed before the buffer was full")
	}
	s.Out.WriteString("yy")
	if len(out()) < bufSize {
		t.Error("a full buffer must be written")
	}
}

// C21: a reader that stopped ends the run with 141, nothing more written.
func TestStreams_ABrokenPipeEnds141(t *testing.T) {
	e := testkit.New(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	r.Close()
	errs, err := os.Create(filepath.Join(e.Tmp, "err"))
	if err != nil {
		t.Fatal(err)
	}
	defer errs.Close()
	var code int
	exit = func(c int) { code = c; panic("exit") }
	defer func() { exit = os.Exit }()
	run := func(f func()) (aborted bool) {
		defer func() { aborted = recover() != nil }()
		f()
		return false
	}
	s := NewStreams(os.Stdin, w, errs)
	s.Out.WriteString("hello")
	if err := s.Flush(); !errors.Is(err, syscall.EPIPE) { // Main turns this into 141 when it is the last flush
		t.Errorf("Flush = %v, want EPIPE", err)
	}
	if !run(func() { s.Out.WriteString(strings.Repeat("x", bufSize)) }) || code != 141 {
		t.Errorf("a write that had to be flushed: code %d", code)
	}
	direct := NewStreams(os.Stdin, w, errs)
	direct.Out.direct = true // what a terminal would be
	code = 0
	if !run(func() { direct.Out.WriteString("x") }) || code != 141 {
		t.Errorf("a direct write: code %d", code)
	}
}

// C22, the probe the plan asks for: a process started with fds 0, 1 and 2 closed finds them open on /dev/null.
func TestStreams_ClosedStdFdsAreReopenedByTheRuntime(t *testing.T) {
	e := testkit.New(t)
	res := e.RunCmd(testkit.Opts{Env: map[string]string{"TEXTIO_HELPER": "closed"}},
		"sh", "-c", `exec "$0" -test.run='^$' <&- >&- 2>&-`, os.Args[0])
	if res.Code != 0 {
		t.Errorf("exit %d (bit 0-2: fd 0-2 found closed, bit 3: write failed)", res.Code)
	}
}

// C21: ShowDiff's flush (small diff, buffer not full) on a pipe nobody reads exits 141, as Python's flush() raising
// BrokenPipeError does. `| true` is the reader that is gone before the child writes.
func TestShowDiff_ABrokenPipeOnFlushEnds141(t *testing.T) {
	e := testkit.New(t)
	res := e.RunCmd(testkit.Opts{Env: map[string]string{"TEXTIO_HELPER": "diffpipe"}}, "bash", "-c",
		`"$0" -test.run='^$' | true; exit ${PIPESTATUS[0]}`, os.Args[0])
	if res.Code != 141 {
		t.Errorf("exit %d, want 141; stderr %q", res.Code, res.Stderr)
	}
}
