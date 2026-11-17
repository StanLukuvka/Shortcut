package commands

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/commands/completion"
	"github.com/StanLukuvka/Shortcut/internal/terminal"
	"github.com/StanLukuvka/Shortcut/internal/terminal/argparse"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// The static completion scripts name every command and alias, the options of the commands that take names, and the
// shells `completion` offers.
func TestCompletion_FollowsTheTable(t *testing.T) {
	has := func(text, word string) bool { // a whole word: "r" is not the "-r" of a fish option
		return regexp.MustCompile(`(^|[^\w-])` + regexp.QuoteMeta(word) + `([^\w-]|$)`).MatchString(text)
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		b, err := completion.Script(shell)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		for _, c := range commands() {
			for _, w := range append([]string{c.Name}, c.Aliases...) {
				if !has(text, w) {
					t.Errorf("%s: no command word %q", shell, w)
				}
			}
			var words []string
			for _, a := range c.Args {
				switch {
				case a.Choices != nil:
					words = append(words, a.Choices...)
				case a.Flags != nil && slices.ContainsFunc(c.Args, func(p terminal.Arg) bool { return p.Flags == nil && (p.Nargs == '+' || p.Nargs == '*') }):
					flag := a.Flags[0]
					if shell == "fish" { // fish spells -y as -s y and --json as -l json
						flag = "-l " + strings.TrimPrefix(flag, "--")
						if !strings.HasPrefix(a.Flags[0], "--") {
							flag = "-s " + strings.TrimPrefix(a.Flags[0], "-")
						}
					}
					words = append(words, flag)
				}
			}
			for _, w := range words {
				if !has(text, w) {
					t.Errorf("%s: %s: no %q", shell, c.Name, w)
				}
			}
		}
	}
}

func TestMain(m *testing.M) { testkit.Main(m) }

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

// Every command's parser prints the help the table renders on -h.
func TestParsers_HelpIsTheTable(t *testing.T) {
	for _, c := range commands() {
		_, err := c.Parse([]string{"-h"})
		ee, ok := err.(*argparse.ExitError)
		if !ok || ee.Code != 0 || ee.Stdout != c.HelpText() || ee.Stderr != "" {
			t.Errorf("%s -h: %v", c.Name, err)
		}
	}
}
