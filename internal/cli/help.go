package cli

import (
	"strings"
	"unicode/utf8"
)

// usageNotes ends the usage text. A raw string cannot hold a backtick, so those are joined in.
const usageNotes = `Project shortcuts and namespaces don't run until approved with ` + "`" + `shortcut update` + "`" + `,
and must be approved again after any change to their definitions. Approval covers
.shortcuts, .shortcuts.local, imports, the scripts they name and each namespace's
scripts/<ns>/ folder, not the programs those call. A change shortcut makes to .shortcuts.local or a namespace keeps its approval.
Global shortcuts have no approval step, so a global link target can change freely.
Set SHORTCUT_SHOW_DIR=0 to hide the "→ <run directory>" line.
`

// usageText is `shortcut --help`: one line per command, from the table.
func usageText() string {
	var b strings.Builder
	b.WriteString("shortcut - run and manage quick scripts and commands\n\nUsage:\n  shortcut <command> [args...]\n  shortcut --version\n\nCommands:\n")
	for _, c := range commands() {
		b.WriteString("  " + padRight(c.words(", "), 12) + "  " + c.Summary + "\n")
	}
	return b.String() + "\n`shortcut help <command>` lists a command's arguments.\n\n" + usageNotes
}

// usage is the command's usage line, after "shortcut ".
func (c Command) usage() string {
	if c.Usage != "" {
		return c.Usage
	}
	parts := []string{c.words("|")}
	for _, a := range c.Args {
		switch {
		case a.Flags != nil:
			parts = append(parts, "["+a.spelling(a.Flags[:1])+"]")
		case a.Nargs == '?':
			parts = append(parts, "["+a.Dest+"]")
		case a.Nargs == '*':
			parts = append(parts, "["+a.Dest+"...]")
		case a.Nargs == '+':
			parts = append(parts, a.Dest+"...")
		default:
			parts = append(parts, a.Dest)
		}
	}
	return strings.Join(parts, " ")
}

// usageLine is the usage line as help prints it, and as a command error ends with it.
func (c Command) usageLine() string { return "usage: shortcut " + c.usage() }

// spelling is the argument as the options list shows it: -y, --yes / --desc TEXT / --names [NAMESPACE] / name.
func (a Arg) spelling(flags []string) string {
	s := a.Dest
	if flags != nil {
		s = strings.Join(flags, ", ")
	}
	switch a.Takes {
	case oneValue:
		s += " " + a.Meta
	case optValue:
		s += " [" + a.Meta + "]"
	}
	return s
}

// helpText is what `shortcut <name> -h` and `shortcut help <name>` print; name is a command or an alias.
func helpText(name string) string {
	c := find(name)
	var b strings.Builder
	b.WriteString(c.usageLine() + "\n")
	if c.Help != "" {
		b.WriteString("\n" + c.Help + "\n")
	}
	for _, title := range []string{"arguments", "options"} {
		positional := title == "arguments"
		var rows [][2]string
		width := 0
		for _, a := range c.Args {
			if (a.Flags == nil) == positional && a.Help != "" {
				rows = append(rows, [2]string{a.spelling(a.Flags), a.Help})
				width = max(width, utf8.RuneCountInString(rows[len(rows)-1][0]))
			}
		}
		if rows != nil {
			b.WriteString("\n" + title + ":\n")
		}
		for _, r := range rows {
			b.WriteString("  " + padRight(r[0], width) + "  " + r[1] + "\n")
		}
	}
	return b.String()
}
