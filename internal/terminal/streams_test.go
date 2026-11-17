package terminal

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// The test binary doubles as the program under test for what only a process can show: TERM_HELPER names the job.
func TestMain(m *testing.M) { testkit.HelperMain(m, "TERM_HELPER", helper) }

func helper(job string) {
	s := NewStreams(os.Stdin, os.Stdout, os.Stderr)
	switch job {
	case "confirm":
		err := Confirmed(s, Confirm{Question: "run 'go'?", NoTerminal: "no terminal", Message: "will run \x1b[2Kit"}, false)
		s.Out.WriteString(fmt.Sprintf("err=%v\n", err))
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
