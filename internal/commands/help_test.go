package commands

import (
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// Every command's `help <cmd>` and `<cmd> -h` print what the table renders, and nothing else; an alias is its command.
// help itself is the exception: `help -h` is the usage text.
func TestHelp_LiveOutputIsTheTable(t *testing.T) {
	e := testkit.New(t)
	for _, c := range commands() {
		for _, name := range append([]string{c.Name}, c.Aliases...) {
			argvs := [][]string{{"help", name}}
			if c.Name != "help" {
				argvs = append(argvs, []string{name, "-h"}, []string{name, "--help"})
			}
			for _, argv := range argvs {
				if res := e.Run(argv...); res.Code != 0 || res.Stdout != c.HelpText() || res.Stderr != "" {
					t.Errorf("%v: exit %d stderr %q stdout %q", argv, res.Code, res.Stderr, res.Stdout)
				}
			}
		}
	}
}

// The words that show the whole usage and the version.
func TestDispatch_UsageAndVersion(t *testing.T) {
	e := testkit.New(t)
	for _, argv := range [][]string{{}, {"-h"}, {"--help"}, {"help"}, {"help", "nosuch"}} {
		res := e.Run(argv...)
		if res.Code != 0 || res.Stdout != usageText() || res.Stderr != "" {
			t.Errorf("%v: exit %d stderr %q", argv, res.Code, res.Stderr)
		}
	}
	if !strings.HasPrefix(usageText(), "shortcut - run and manage quick scripts and commands\n\nUsage:\n") || !strings.HasSuffix(usageText(), "line.\n") {
		t.Errorf("usageText is not the usage text: %q", usageText())
	}
	if res := e.Run("--version"); res.Code != 0 || res.Stdout != "shortcut 0.1.0\n" || Version != "0.1.0" {
		t.Errorf("--version: exit %d stdout %q", res.Code, res.Stdout)
	}
	// An empty word is an unknown command, not a crash.
	if res := e.Run(""); res.Code != 2 || !strings.HasPrefix(res.Stderr, "error: unknown command ''") {
		t.Errorf("empty word: exit %d stderr %q", res.Code, res.Stderr)
	}
}
