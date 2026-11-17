package commands

import (
	"fmt"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/terminal"
	"github.com/StanLukuvka/Shortcut/internal/terminal/argparse"
)

// usageText is `shortcut --help`: one line per command, from the table.
func usageText() string {
	var b strings.Builder
	b.WriteString("shortcut - run and manage quick scripts and commands\n\nUsage:\n  shortcut <command> [args...]\n  shortcut --version\n\nCommands:\n")
	for _, c := range commands() {
		b.WriteString("  " + fmt.Sprintf("%-12s", c.Words(", ")) + "  " + c.Summary + "\n")
	}
	return b.String() + "\n`shortcut help <command>` lists a command's arguments.\n\nSet SHORTCUT_SHOW_DIR=0 to hide the \"→ <run directory>\" line.\n"
}

// cmdHelp prints a command's help, or the usage text.
func cmdHelp(_ *store.Context, s *terminal.Streams, _ terminal.Command, args []string) error {
	if len(args) > 0 {
		c := find(args[0])
		if c == nil {
			return argparse.UsageError("help", "unknown command '"+args[0]+"'")
		}
		s.Out.WriteString(c.HelpText())
		return nil
	}
	s.Out.WriteString(usageText())
	return nil
}
