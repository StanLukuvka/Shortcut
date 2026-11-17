package terminal

import (
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestConfirmed_WithoutATerminalAndWithYes(t *testing.T) {
	e := testkit.New(t)
	c := Confirm{Question: "q?", NoTerminal: "run it in a terminal, or pass -y", Message: "this changes things"}
	s, out, errs := capture(t, e, "y\n") // stdin is a file: no terminal, so the input is never read
	if err := Confirmed(s, c, false); err == nil || err.Error() != c.NoTerminal {
		t.Errorf("no terminal: %v", err)
	}
	if err := Confirmed(s, c, true); err != nil {
		t.Errorf("-y: %v", err)
	}
	if out() != "" || errs() != "" {
		t.Errorf("-y without TellOnYes prints nothing: %q %q", out(), errs())
	}
	c.TellOnYes = true
	if err := Confirmed(s, c, true); err != nil {
		t.Errorf("-y: %v", err)
	}
	if out() != "this changes things\n" {
		t.Errorf("TellOnYes: %q", out())
	}
}

func TestConfirmed_ATerminalSession(t *testing.T) {
	e := testkit.New(t)
	run := func(keys []testkit.Key) string {
		code, out := e.InTerminalCmd(testkit.Opts{Env: map[string]string{"TERM_HELPER": "confirm"}}, keys, testkit.SelfArgs()...)
		if code != 0 {
			t.Fatalf("exit %d: %q", code, out)
		}
		return out
	}
	// The message sits above the question, it is written as it is, and `[y/N]` marks the default.
	for _, c := range []struct {
		name  string
		input string
		yes   bool
	}{
		{"y", "y\n", true}, {"YES", " YES \n", true}, {"n", "n\n", false}, {"maybe", "maybe\n", false},
		{"empty line", "\n", false}, {"unfinished", "y\x04\x04", true}, // an unfinished last line counts: the first Ctrl-D flushes "y", the second is end of input
		{"end of input", "\x04", false}, // Ctrl-D
	} {
		out := run([]testkit.Key{{Wait: "[y/N]", Send: []byte(c.input)}})
		if !strings.Contains(out, "will run \x1b[2Kit\r\nrun 'go'? [y/N] ") {
			t.Errorf("%s: message and question: %q", c.name, out)
		}
		if c.yes != (strings.Contains(out, "err=<nil>") && !strings.Contains(out, "Aborted.")) {
			t.Errorf("%s: answer: %q", c.name, out)
		}
		if !c.yes && !strings.Contains(out, "Aborted.") {
			t.Errorf("%s: no Aborted: %q", c.name, out)
		}
	}
}
