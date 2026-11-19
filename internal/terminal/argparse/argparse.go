// Package argparse is the argument parser of shortcut's commands.
package argparse

import (
	"slices"
	"strings"
)

// Parse takes "--opt value", "--opt=value", short flags ("-y"; they are not combined), "--", and positional words
// anywhere between the options. An option matches by its full name only. Options have no types and none is required;
// a command has no subcommands.

type kind int

const (
	kStore    kind = iota // --opt VALUE
	kTrue                 // --opt
	kOptional             // --opt [VALUE], "" when there is none
)

// Action is one argument of a parser: an option when flags is set, else a positional.
type Action struct {
	flags   []string
	dest    string
	kind    kind // options
	nargs   byte // positional: 0 is exactly one word, '?' one or none, '*' any, '+' at least one
	choices []string
}

type Parser struct {
	cmd     string                  // the command word, for the Try line
	help    func(cmd string) string // the -h text of a command
	actions []*Action
	byFlag  map[string]*Action
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

// Flag is an option without a value.
func Flag(dest string, flags ...string) *Action {
	return &Action{flags: flags, dest: dest, kind: kTrue}
}

// Value is --opt VALUE.
func Value(dest string, flags ...string) *Action {
	return &Action{flags: flags, dest: dest, kind: kStore}
}

// Optional is --opt [VALUE]: "" when it has no value.
func Optional(dest string, flags ...string) *Action {
	return &Action{flags: flags, dest: dest, kind: kOptional}
}

// Positional takes nargs 0 (one word), '?', '*' or '+'.
func Positional(dest string, nargs byte) *Action { return &Action{dest: dest, nargs: nargs} }

// Choice is a positional that must be one of choices.
func Choice(dest string, choices ...string) *Action { return &Action{dest: dest, choices: choices} }

// Namespace maps the dest of each argument to its value: a string, a bool, a []string (positional words) or nil
// (not given).
type Namespace map[string]any

// Str is a string value, "" when it was not given.
func (n Namespace) Str(dest string) string { s, _ := n[dest].(string); return s }

// Set reports whether a value was given (an empty one counts).
func (n Namespace) Set(dest string) bool { _, ok := n[dest].(string); return ok }

func (n Namespace) Bool(dest string) bool { b, _ := n[dest].(bool); return b }

// List is the words of a positional that takes several.
func (n Namespace) List(dest string) []string { l, _ := n[dest].([]string); return l }

// ExitError ends a command with a code and, optionally, text for stdout and stderr, which Main prints as it is
// (so a message carries its own "error: " and newline). Code 0 is -h; 2 is a usage error; 1 is terminal.Die.
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
		ns[a.dest] = nil
		if a.flags == nil {
			pos = a // one positional per parser, as every command has
		} else if a.kind == kTrue {
			ns[a.dest] = false
		}
	}
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
				ns[act.dest] = val
			}
		}
	}
	if err := p.assign(ns, pos, words); err != nil {
		return nil, err
	}
	return ns, nil
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
		if pos.choices != nil && !slices.Contains(pos.choices, w) {
			return UsageError(p.cmd, "invalid choice '"+w+"' (choose from "+strings.Join(pos.choices, ", ")+")")
		}
	}
	if pos.nargs == '*' || pos.nargs == '+' {
		ns[pos.dest] = slices.Clone(words)
	} else {
		ns[pos.dest] = words[0]
	}
	return nil
}
