package commands

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestOutputSafety_StoredTextCannotRewriteTheTerminal(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{
		"go": map[string]any{"description": "clean\x1b[2K\rfaked \u009b1m ‮gnp", "inline": "printf '\\033[31mred\\033[0m'"},
	})
	for _, args := range [][]string{{"list"}, {"show", "go"}, {"show", "--json", "go"}} {
		res := e.Run(args...)
		res.WantCode(t, 0)
		for _, c := range []string{"\x1b", "\r", "\u009b", "‮"} {
			if strings.Contains(res.Stdout+res.Stderr, c) {
				t.Errorf("%v: output has %q", args, c)
			}
		}
	}
	testkit.Contains(t, "show", e.Run("show", "go").Stdout, "clean^[[2K^Mfaked")
	// A shortcut's own output is its own business
	if got := e.Run("r", "go").Stdout; got != "\x1b[31mred\x1b[0m" {
		t.Errorf("run: %q", got)
	}
}

func TestOutputSafety_ATerminalPromptCannotBeForged(t *testing.T) {
	e := testkit.New(t)
	proj := e.Folder("proj\x1b[2Kx")
	e.WriteStore(e.Store, map[string]any{"go": map[string]any{"inline": "echo ran", "confirm": true, "cwd": proj}})
	_, out := e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "[y/N]", Send: y("n\n")}}, "r", "go")
	if strings.Contains(out, "\x1b[2Kx") {
		t.Errorf("the escape reached the terminal:\n%q", out)
	}
	testkit.Contains(t, "prompt", out, "^[[2Kx")
}

func TestOutputSafety_HelpStaysPlainOnATerminal(t *testing.T) {
	e := testkit.New(t)
	rc, out := e.InTerminal(testkit.Opts{}, nil, "new", "--help")
	if rc != 0 {
		t.Errorf("new --help: exit %d\n%s", rc, out)
	}
	if strings.Contains(out, "^[") || strings.Contains(out, "\x1b") {
		t.Errorf("new --help is not plain:\n%q", out)
	}
}

func TestOutputSafety_EscapeCodesInAListedPathCannotTouchTheTerminal(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"tool": map[string]any{"script": "bin/t\x1b[2Jx"}}, testkit.Files(map[string]any{"bin/t\x1b[2Jx": "echo hi\n"}))
	res := e.RunWith(in(proj), "update", "-y")
	if strings.Contains(res.Stdout, "\x1b") {
		t.Errorf("escape in the list: %q", res.Stdout)
	}
	testkit.Contains(t, "list", res.Stdout, "  new      bin/t^[[2Jx")
}

func TestOutputSafety_AClosedStreamChangesNothing(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, testkit.GO)
	for _, script := range []string{"shortcut list >&-", "shortcut r go 2>&-"} {
		if res := e.Shell(testkit.Opts{}, script); res.Code != 0 {
			t.Errorf("%s: exit %d %s", script, res.Code, res.Stderr)
		}
	}
}

// A byte 0x9b in a script is CSI on an 8-bit terminal: show prints the replacement character, never the byte.
func TestOutputSafety_ABadByteInAScriptNeverReachesTheTerminal(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"go": map[string]any{"script": "scripts/go.sh"}})
	e.WriteFile(filepath.Join(e.Store, "scripts", "go.sh"), "echo \x9b[31mred\x1b[2K\n", 0)
	res := e.Run("show", "go")
	res.WantCode(t, 0)
	for _, c := range []string{"\x9b", "\x1b"} {
		if strings.Contains(res.Stdout+res.Stderr, c) {
			t.Errorf("show: output has %q:\n%q", c, res.Stdout)
		}
	}
	testkit.Contains(t, "show", res.Stdout, "echo \uFFFD[31mred^[[2K")
}
