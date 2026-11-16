package cli

import (
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// Each command is complete, and no word names two commands.
func TestCommands_TableIsComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range commands() {
		if c.Summary == "" || c.Run == nil || !strings.Contains(usageText(), c.Summary) {
			t.Errorf("%s: no summary, no handler, or not in the usage text", c.Name)
		}
		for _, w := range append([]string{c.Name}, c.Aliases...) {
			if seen[w] {
				t.Errorf("%q names two commands", w)
			}
			seen[w] = true
		}
	}
}

// run reads its own words, so its table row only describes them: each option it lists must be one it reads before the name.
func TestCommands_RunReadsTheOptionsItLists(t *testing.T) {
	e := testkit.New(t)
	for _, a := range find("run").Args {
		for _, flag := range a.Flags {
			if res := e.Run("run", flag); res.Code != 2 || !strings.HasPrefix(res.Stderr, "error: missing shortcut name\n") {
				t.Errorf("run %s: exit %d stderr %q", flag, res.Code, res.Stderr)
			}
		}
	}
}
