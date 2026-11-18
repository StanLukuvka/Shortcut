package commands

import (
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// Every command's `help <cmd>` and `<cmd> -h` (`--help` is argparse's) print what the table renders, and nothing else; an alias is its command.
// help itself is the exception: `help -h` is the usage text.
func TestHelp_LiveOutputIsTheTable(t *testing.T) {
	e := testkit.New(t)
	for _, c := range commands() {
		for _, name := range append([]string{c.Name}, c.Aliases...) {
			argvs := [][]string{{"help", name}}
			if c.Name != "help" {
				argvs = append(argvs, []string{name, "-h"})
			}
			for _, argv := range argvs {
				e.Run(argv...).Want(t, 0, c.HelpText(), "")
			}
		}
	}
}

// The words that show the whole usage and the version.
func TestDispatch_UsageAndVersion(t *testing.T) {
	e := testkit.New(t)
	for _, argv := range [][]string{{}, {"-h"}, {"--help"}, {"help"}} {
		e.Run(argv...).Want(t, 0, usageText(), "")
	}
	e.Run("help", "nosuch").Want(t, 2, "", "error: unknown command 'nosuch'\nTry 'shortcut help -h' for more information.\n")
	if !strings.HasPrefix(usageText(), "shortcut - run and manage quick scripts and commands\n\nUsage:\n") || !strings.HasSuffix(usageText(), "line.\n") {
		t.Errorf("usageText is not the usage text: %q", usageText())
	}
	if res := e.Run("--version"); res.Code != 0 || res.Stdout != "shortcut 1.0.0\n" || Version != "1.0.0" {
		t.Errorf("--version: exit %d stdout %q", res.Code, res.Stdout)
	}
	// An empty word is an unknown command, not a crash.
	if res := e.Run(""); res.Code != 2 || !strings.HasPrefix(res.Stderr, "error: unknown command ''") {
		t.Errorf("empty word: exit %d stderr %q", res.Code, res.Stderr)
	}
}
