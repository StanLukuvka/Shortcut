package cli

import (
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// py: test_output_safety.py::test_stored_text_cannot_rewrite_the_terminal
func TestOutputSafety_StoredTextCannotRewriteTheTerminal(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{
		"go": map[string]any{"description": "clean\x1b[2K\rfaked \u009b1m ‮gnp", "inline": "printf '\\033[31mred\\033[0m'"},
	})
	for _, args := range [][]string{{"list"}, {"show", "go"}, {"show", "--json", "go"}} {
		res := e.Run(args...)
		if res.Code != 0 {
			t.Errorf("%v: exit %d %s", args, res.Code, res.Stderr)
		}
		for _, c := range []string{"\x1b", "\r", "\u009b", "‮"} {
			if strings.Contains(res.Stdout+res.Stderr, c) {
				t.Errorf("%v: output has %q", args, c)
			}
		}
	}
	contains(t, "show", e.Run("show", "go").Stdout, "clean^[[2K^Mfaked")
	// A shortcut's own output is its own business
	if got := e.Run("r", "go").Stdout; got != "\x1b[31mred\x1b[0m" {
		t.Errorf("run: %q", got)
	}
}

// py: test_output_safety.py::test_a_terminal_prompt_cannot_be_forged
func TestOutputSafety_ATerminalPromptCannotBeForged(t *testing.T) {
	e := testkit.New(t)
	proj := e.Folder("proj\x1b[2Kx")
	e.WriteStore(e.Store, map[string]any{"go": map[string]any{"inline": "echo ran", "confirm": true, "cwd": proj}})
	_, out := e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "[y/N]", Send: y("n\n")}}, "r", "go")
	if strings.Contains(out, "\x1b[2Kx") {
		t.Errorf("the escape reached the terminal:\n%q", out)
	}
	contains(t, "prompt", out, "^[[2Kx")
}

// py: test_output_safety.py::test_help_stays_plain_on_a_terminal
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

// py: test_output_safety.py::test_escape_codes_in_a_diff_cannot_touch_the_terminal
func TestOutputSafety_EscapeCodesInADiffCannotTouchTheTerminal(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(testkit.BinTool, testkit.Files(map[string]any{"bin/tool": "echo \x1b[2Jhidden\r\n"}))
	res := e.RunWith(in(proj), "update", "-y")
	if strings.Contains(res.Stdout, "\x1b") {
		t.Errorf("escape in the diff: %q", res.Stdout)
	}
	contains(t, "diff", res.Stdout, "^[[2Jhidden")
}

// py: test_output_safety.py::test_a_closed_stream_changes_nothing
func TestOutputSafety_AClosedStreamChangesNothing(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, testkit.GO)
	for _, script := range []string{"shortcut list >&-", "shortcut r go 2>&-"} {
		if res := e.Shell(testkit.Opts{}, script); res.Code != 0 {
			t.Errorf("%s: exit %d %s", script, res.Code, res.Stderr)
		}
	}
}
