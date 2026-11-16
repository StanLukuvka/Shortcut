package cli

import (
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestApproval_PromptInTerminalAndColour(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(testkit.HI)
	o := in(proj)
	rc, out := e.InTerminal(o, []testkit.Key{{Wait: "approve? [y/N]", Send: y("n\n")}}, "update")
	if rc != 1 {
		t.Errorf("n: exit %d\n%s", rc, out)
	}
	testkit.Contains(t, "n", out, "Aborted.")
	if code := e.RunWith(o, "r", "hi").Code; code != 1 {
		t.Errorf("run after refusing: exit %d", code)
	}
	rc, out = e.InTerminal(o, []testkit.Key{{Wait: "approve? [y/N]", Send: y("y\n")}}, "update")
	if rc != 0 {
		t.Errorf("y: exit %d\n%s", rc, out)
	}
	if got := e.RunWith(in(proj), "r", "hi").Stdout; got != "hi\n" {
		t.Errorf("run after approving: %q", got)
	}
	if got := e.RunWith(in(proj), "update", ".").Stdout; strings.Contains(got, "\x1b[") {
		t.Errorf("colour without a terminal: %q", got)
	}
}
