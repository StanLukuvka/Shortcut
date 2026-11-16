package term

import (
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/term/argparse"
)

// Takes is what an option does with the words after it.
type Takes uint8

const (
	NoValue  Takes = iota // --json
	OneValue              // --desc TEXT
	OptValue              // --names [NAMESPACE]
)

// Arg is an option, or a positional when Flags is empty.
type Arg struct {
	Dest    string   // the key in the parsed Namespace
	Flags   []string // the spellings of an option
	Takes   Takes    // option: what follows it
	Meta    string   // option: the value's name in help
	Nargs   byte     // positional: 0 one word, '?', '*' or '+'
	Choices []string // positional: the words it may be
	Help    string   // one line, in `help <command>`
}

// Command is one command, defined once: dispatch, its parser, the usage text and `help <name>` are read
// from it. The completion scripts are static; a test fails if they disagree with it.
type Command struct {
	Name    string
	Aliases []string
	Summary string // one line, in the usage text
	Help    string // what `help <name>` says after the usage line
	Usage   string // the usage line, when the arguments don't give it
	Args    []Arg  // options and positionals; run and help read their own words, so theirs only describe
}

// Words is the name and the aliases, joined.
func (c Command) Words(sep string) string {
	return strings.Join(append([]string{c.Name}, c.Aliases...), sep)
}

// Parse parses a command's words with the parser its Args describe. Its -h prints HelpText.
func (c Command) Parse(argv []string) (Namespace, error) {
	return c.parser().Parse(argv)
}

func (c Command) parser() *argparse.Parser {
	acts := make([]*argparse.Action, len(c.Args))
	for i, a := range c.Args {
		switch {
		case a.Choices != nil:
			acts[i] = argparse.Choice(a.Dest, a.Choices...)
		case a.Flags == nil:
			acts[i] = argparse.Positional(a.Dest, a.Nargs)
		case a.Takes == OneValue:
			acts[i] = argparse.Value(a.Dest, a.Flags...)
		case a.Takes == OptValue:
			acts[i] = argparse.Optional(a.Dest, a.Flags...)
		default:
			acts[i] = argparse.Flag(a.Dest, a.Flags...)
		}
	}
	return argparse.New(c.Name, func(string) string { return c.HelpText() }, acts...)
}
