package cli

import (
	"path/filepath"
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
	o.Env = map[string]string{"PAGER": ""}
	rc, out = e.InTerminal(o, []testkit.Key{{Wait: "approve? [y/N]", Send: y("y\n")}}, "update")
	if rc != 0 {
		t.Errorf("y: exit %d\n%s", rc, out)
	}
	testkit.Contains(t, "y", out, "\x1b[32m+")
	if got := e.RunWith(in(proj), "r", "hi").Stdout; got != "hi\n" {
		t.Errorf("run after approving: %q", got)
	}
	if got := e.RunWith(in(proj), "update", ".").Stdout; strings.Contains(got, "\x1b[") {
		t.Errorf("colour without a terminal: %q", got)
	}
}

func TestApproval_ABinaryNeedsTypingOutAtThePrompt(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(testkit.BinTool, testkit.Files(map[string]any{"bin/tool": []byte("\x7fELF\x00\x00")}))
	o := in(proj)
	rc, out := e.InTerminal(o, []testkit.Key{{Wait: "Type binary", Send: y("y\n")}}, "update")
	if rc != 1 {
		t.Errorf("y: exit %d\n%s", rc, out)
	}
	testkit.Contains(t, "y", out, "Aborted.")
	rc, out = e.InTerminal(o, []testkit.Key{{Wait: "Type binary", Send: y("binary\n")}}, "update")
	if rc != 0 {
		t.Errorf("binary: exit %d\n%s", rc, out)
	}
	testkit.Contains(t, "binary", out, "Approved")
	// Once seen and unchanged, it's an ordinary prompt
	e.WriteFile(filepath.Join(proj, "bin", "other"), "echo hi\n", 0)
	e.WriteStore(filepath.Join(proj, ".shortcuts"), map[string]any{"tool": map[string]any{"script": "bin/tool"}, "other": map[string]any{"script": "bin/other"}})
	rc, out = e.InTerminal(o, []testkit.Key{{Wait: "approve? [y/N]", Send: y("y\n")}}, "update")
	if rc != 0 {
		t.Errorf("ordinary prompt: exit %d\n%s", rc, out)
	}
}
