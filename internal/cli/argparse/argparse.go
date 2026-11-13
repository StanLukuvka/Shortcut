// Package argparse is the argument parser of shortcut's commands.
package argparse

import (
	"strings"
)

// The parser takes `--opt value`, `--opt=value`, short flags (`-y`, never combined), `--`, and positionals anywhere
// between the options. An option matches by its full name only. Types, required options and subparsers are not
// supported: no command uses them.

type kind int

const (
	kStore    kind = iota // --opt VALUE
	kTrue                 // --opt
	kOptional             // --opt [VALUE], "" when there is none
	kAppend               // --opt VALUE, repeatable
)

// Action is one argument of a parser: an option when flags is set, else a positional.
type Action struct {
	flags   []string
	dest    string
	kind    kind // options
	nargs   byte // positionals: 0 = exactly one, '?', '*' or '+'
	def     any  // positionals: the value when none is given
	choices []string
}

func (a *Action) name() string {
	if a.flags != nil {
		return strings.Join(a.flags, "/")
	}
	return a.dest
}

type Parser struct {
	cmd       string                  // the command word, for the Try line
	help      func(cmd string) string // the -h text of a command
	actions   []*Action
	byFlag    map[string]*Action
	exclusive [][]string // groups of dests of which at most one may be given
}

// New makes the parser of one command, with its -h. help gives the -h text of the command.
func New(cmd string, help func(cmd string) string, add ...*Action) *Parser {
	p := &Parser{cmd: cmd, help: help, actions: add, byFlag: map[string]*Action{}}
	for _, a := range add {
		for _, f := range a.flags {
			p.byFlag[f] = a
		}
	}
	return p
}

// Exclusive makes the options with these dests mutually exclusive.
func (p *Parser) Exclusive(dests ...string) *Parser {
	p.exclusive = append(p.exclusive, dests)
	return p
}

// Flag is an option without a value.
func Flag(dest string, flags ...string) *Action {
	return &Action{flags: flags, dest: dest, kind: kTrue}
}

// Value is --opt VALUE.
func Value(dest string, flags ...string) *Action {
	return &Action{flags: flags, dest: dest, kind: kStore}
}

// Repeated is --opt VALUE, given any number of times.
func Repeated(dest string, flags ...string) *Action {
	return &Action{flags: flags, dest: dest, kind: kAppend}
}

// Optional is --opt [VALUE]: "" when it has no value.
func Optional(dest string, flags ...string) *Action {
	return &Action{flags: flags, dest: dest, kind: kOptional}
}

// Positional takes nargs 0 (one word), '?', '*' or '+'.
func Positional(dest string, nargs byte) *Action { return &Action{dest: dest, nargs: nargs} }

// Choice is a positional that must be one of choices: def when it is left out, or required when def is "".
func Choice(dest, def string, choices ...string) *Action {
	a := &Action{dest: dest, def: def, choices: choices}
	if def != "" {
		a.nargs = '?'
	}
	return a
}

// Namespace maps the dest of each argument to its value, which is a string, a bool, a []string or
// nil (not given).
type Namespace map[string]any

// Str is a string value, "" when it was not given.
func (n Namespace) Str(dest string) string { s, _ := n[dest].(string); return s }

// Set reports whether a value was given (an empty one counts).
func (n Namespace) Set(dest string) bool { _, ok := n[dest].(string); return ok }

func (n Namespace) Bool(dest string) bool { b, _ := n[dest].(bool); return b }

// List is a repeated option's values, or a positional with several.
func (n Namespace) List(dest string) []string { l, _ := n[dest].([]string); return l }

// ExitError is what a command exits with: 2 and a message on stderr, or 0 and the help on stdout for -h.
// The caller prints Stdout and Stderr as they are and exits with Code.
type ExitError struct {
	Code           int
	Stdout, Stderr string
}

func (e *ExitError) Error() string { return e.Stderr }

// UsageError is a usage error of a command: exit 2, "error: msg" and a line pointing to the command's -h.
func UsageError(cmd, msg string) *ExitError {
	return &ExitError{Code: 2, Stderr: "error: " + msg + "\nTry 'shortcut " + cmd + " -h' for more information.\n"}
}

// startsOpt: a word that is an option, not a value. A lone "-" is a value.
func startsOpt(s string) bool { return len(s) > 1 && s[0] == '-' }

// Parse returns the namespace, or an *ExitError: 2 for a usage error, 0 for -h.
func (p *Parser) Parse(argv []string) (Namespace, error) {
	ns := Namespace{}
	var pos *Action
	for _, a := range p.actions {
		switch {
		case a.flags == nil:
			pos = a // lazy: one positional per parser, as every command has; a second one would need a fill rule
			ns[a.dest] = a.def
		case a.kind == kTrue:
			ns[a.dest] = false
		case a.kind == kAppend:
			ns[a.dest] = []string{}
		default:
			ns[a.dest] = nil
		}
	}
	given := map[string]bool{}
	var words []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--":
			words = append(words, argv[i+1:]...)
			i = len(argv)
		case a == "-h" || a == "--help":
			return nil, &ExitError{Stdout: p.help(p.cmd)}
		case !startsOpt(a):
			words = append(words, a)
		default:
			name, val, hasVal := a, "", false
			if strings.HasPrefix(a, "--") {
				name, val, hasVal = strings.Cut(a, "=")
			}
			act := p.byFlag[name]
			if act == nil {
				return nil, UsageError(p.cmd, "unrecognized option '"+a+"'")
			}
			given[act.dest] = true
			switch act.kind {
			case kTrue:
				if hasVal {
					return nil, UsageError(p.cmd, "option '"+name+"' takes no value")
				}
				ns[act.dest] = true
			case kOptional:
				if !hasVal && i+1 < len(argv) && argv[i+1] != "--" && !startsOpt(argv[i+1]) {
					i++
					val = argv[i]
				}
				ns[act.dest] = val
			default:
				if !hasVal {
					if i+1 >= len(argv) || startsOpt(argv[i+1]) || argv[i+1] == "--" {
						return nil, UsageError(p.cmd, "option '"+name+"' needs a value")
					}
					i++
					val = argv[i]
				}
				if act.kind == kAppend {
					ns[act.dest] = append(ns.List(act.dest), val)
				} else {
					ns[act.dest] = val
				}
			}
		}
	}
	if err := p.assign(ns, pos, words); err != nil {
		return nil, err
	}
	for _, group := range p.exclusive {
		var set []string
		for _, d := range group {
			if given[d] {
				set = append(set, p.longest(d))
			}
		}
		if len(set) > 1 {
			return nil, UsageError(p.cmd, strings.Join(set, " and ")+" cannot be used together")
		}
	}
	return ns, nil
}

// longest is the last option string of the option with this dest ("--yes" of -y, --yes).
func (p *Parser) longest(dest string) string {
	for _, a := range p.actions {
		if a.dest == dest && a.flags != nil {
			return a.flags[len(a.flags)-1]
		}
	}
	return dest
}

// assign gives the positional words to the positional, by its nargs.
func (p *Parser) assign(ns Namespace, pos *Action, words []string) error {
	max := 0
	switch {
	case pos == nil:
	case pos.nargs == '*' || pos.nargs == '+':
		max = len(words)
	default:
		max = 1
	}
	if len(words) > max {
		return UsageError(p.cmd, "unexpected argument '"+words[max]+"'")
	}
	if pos == nil {
		return nil
	}
	if len(words) == 0 {
		if pos.nargs == 0 || pos.nargs == '+' {
			return UsageError(p.cmd, "missing "+pos.dest)
		}
		if pos.nargs == '*' {
			ns[pos.dest] = []string{}
		}
		return nil
	}
	for _, w := range words {
		if pos.choices == nil {
			break
		}
		ok := false
		for _, c := range pos.choices {
			ok = ok || c == w
		}
		if !ok {
			return UsageError(p.cmd, "invalid choice '"+w+"' (choose from "+strings.Join(pos.choices, ", ")+")")
		}
	}
	if pos.nargs == '*' || pos.nargs == '+' {
		ns[pos.dest] = append([]string{}, words...)
	} else {
		ns[pos.dest] = words[0]
	}
	return nil
}
