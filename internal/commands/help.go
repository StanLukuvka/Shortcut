package commands

import (
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/term"
)

// usageNotes ends the usage text. A raw string cannot hold a backtick, so those are joined in.
const usageNotes = `Set SHORTCUT_SHOW_DIR=0 to hide the "→ <run directory>" line.
`

// usageText is `shortcut --help`: one line per command, from the table.
func usageText() string {
	var b strings.Builder
	b.WriteString("shortcut - run and manage quick scripts and commands\n\nUsage:\n  shortcut <command> [args...]\n  shortcut --version\n\nCommands:\n")
	for _, c := range commands() {
		b.WriteString("  " + term.PadRight(c.Words(", "), 12) + "  " + c.Summary + "\n")
	}
	return b.String() + "\n`shortcut help <command>` lists a command's arguments.\n\n" + usageNotes
}

// cmdHelp prints a command's help, or the usage text.
func cmdHelp(_ *store.Context, s *term.Streams, _ term.Command, args []string) error {
	text := usageText()
	if len(args) > 0 {
		if c := find(args[0]); c != nil {
			text = c.HelpText()
		}
	}
	s.Out.WriteString(text)
	return nil
}
