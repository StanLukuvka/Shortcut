package terminal

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// usage is the command's usage line, after "shortcut ".
func (c Command) usage() string {
	if c.Usage != "" {
		return c.Usage
	}
	parts := []string{c.Words("|")}
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

// UsageLine is the usage line as help prints it, and as a command error ends with it.
func (c Command) UsageLine() string { return "usage: shortcut " + c.usage() }

// spelling is the argument as the options list shows it: -y, --yes / --desc TEXT / --names [NAMESPACE] / name.
func (a Arg) spelling(flags []string) string {
	s := a.Dest
	if flags != nil {
		s = strings.Join(flags, ", ")
	}
	switch a.Takes {
	case OneValue:
		s += " " + a.Meta
	case OptValue:
		s += " [" + a.Meta + "]"
	}
	return s
}

// HelpText is what `shortcut <name> -h` and `shortcut help <name>` print.
func (c Command) HelpText() string {
	var b strings.Builder
	b.WriteString(c.UsageLine() + "\n")
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
			b.WriteString("  " + fmt.Sprintf("%-*s", width, r[0]) + "  " + r[1] + "\n")
		}
	}
	return b.String()
}
