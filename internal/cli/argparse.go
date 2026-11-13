// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-1). Don't hand-edit without checking the plan.

package cli

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A port of the part of Python 3.14's argparse that shortcut's commands use: the same classification of words as
// options and arguments, abbreviations, `--opt=value`, combined short flags (`-yh`), positionals that an option
// splits (rejected, as argparse rejects them), `--`, mutually exclusive options and the same error messages.
// Not ported (no command uses it): types, required options, subparsers, REMAINDER, deprecated, fromfile, `-5` options.

type kind int

const (
	kStore    kind = iota // --opt VALUE (nargs None)
	kTrue                 // store_true (nargs 0)
	kOptional             // --opt [VALUE] (nargs '?'), konst when there is none
	kAppend               // --opt VALUE, repeatable
	kHelp                 // -h, --help
)

// action is one argument of a parser: an option when flags is set, else a positional.
type action struct {
	flags   []string
	dest    string
	kind    kind   // options
	konst   string // kOptional
	nargs   byte   // positionals: 0 = one, '?', '*' or '+'
	def     any    // positionals: the default
	choices []string
}

func (a *action) name() string {
	if a.flags != nil {
		return strings.Join(a.flags, "/")
	}
	return a.dest
}

type parser struct {
	cmd       string // "shortcut <cmd>" is argparse's prog
	actions   []*action
	byFlag    map[string]*action
	flagOrder []string // option strings in the order they were added: it orders an ambiguity message
	conflicts map[*action][]*action
}

// newParser is Parser(prog="shortcut <cmd>") with its -h: add the arguments in Python's order.
func newParser(cmd string, add ...*action) *parser {
	p := &parser{cmd: cmd, byFlag: map[string]*action{}, conflicts: map[*action][]*action{}}
	for _, a := range append([]*action{{flags: []string{"-h", "--help"}, kind: kHelp}}, add...) {
		p.actions = append(p.actions, a)
		for _, f := range a.flags {
			p.byFlag[f] = a
			p.flagOrder = append(p.flagOrder, f)
		}
	}
	return p
}

// exclusive is add_mutually_exclusive_group over the options with these dests.
func (p *parser) exclusive(dests ...string) *parser {
	var group []*action
	for _, a := range p.actions {
		for _, d := range dests {
			if a.dest == d {
				group = append(group, a)
			}
		}
	}
	for _, a := range group {
		for _, b := range group {
			if a != b {
				p.conflicts[a] = append(p.conflicts[a], b)
			}
		}
	}
	return p
}

func flag(dest string, flags ...string) *action {
	return &action{flags: flags, dest: dest, kind: kTrue}
}
func value(dest string, flags ...string) *action {
	return &action{flags: flags, dest: dest, kind: kStore}
}
func repeated(dest string, flags ...string) *action {
	return &action{flags: flags, dest: dest, kind: kAppend}
}
func optional(dest, konst string, flags ...string) *action {
	return &action{flags: flags, dest: dest, kind: kOptional, konst: konst}
}
func positional(dest string, nargs byte) *action { return &action{dest: dest, nargs: nargs} }

// parsers is the parser of each command that has one, as its cmd_* builds it (the arguments in the order cli.py adds them).
var parsers = map[string]*parser{
	"new": newParser("new", positional("name", 0), value("desc", "--desc"), value("script", "--script"), value("copy", "--copy"),
		value("cwd", "--cwd"), flag("local", "--local"), value("namespace", "--namespace"), value("by", "--by"),
		repeated("arg", "--arg"), repeated("arg_desc", "--arg-desc"), flag("confirm", "--confirm")).exclusive("script", "copy"),
	"list":     newParser("list", optional("names", "", "--names")),
	"show":     newParser("show", positional("name", '+'), flag("json", "--json"), flag("skip_local", "--global")),
	"location": newParser("location", positional("name", '+'), flag("skip_local", "--global")),
	"rm": newParser("rm", positional("name", '*'), value("namespace", "--namespace"), flag("yes", "-y", "--yes"),
		flag("local", "--local"), flag("skip_local", "--global")),
	"promote": newParser("promote", positional("name", 0), value("new_name", "--as"), flag("yes", "-y", "--yes"), flag("force", "--force")),
	"update":  newParser("update", positional("group", '?'), flag("yes", "-y", "--yes")),
	"init": newParser("init", positional("source", '?'), flag("is_global", "--global"), flag("yes", "-y", "--yes"),
		flag("replace", "--replace")),
	"completion": newParser("completion", &action{dest: "shell", nargs: '?', def: "bash", choices: []string{"bash", "fish", "zsh"}}),
}

// Namespace is argparse's Namespace: the dest of each argument to its value, which is a string, a bool, a []string or
// nil (not given). The names are Python's (skip_local, is_global, new_name, arg_desc).
type Namespace map[string]any

// Str is a string value, "" when it was not given.
func (n Namespace) Str(dest string) string { s, _ := n[dest].(string); return s }

// Set reports whether a value was given (an empty one counts).
func (n Namespace) Set(dest string) bool { _, ok := n[dest].(string); return ok }

func (n Namespace) Bool(dest string) bool { b, _ := n[dest].(bool); return b }

// List is a repeated option's values, or a positional with several.
func (n Namespace) List(dest string) []string { l, _ := n[dest].([]string); return l }

// Parse is parse_args: the namespace, or an *ExitError for what argparse exits on: 2 and the usage and message on
// stderr, or 0 and the help on stdout for -h.
func (p *parser) Parse(argv []string) (Namespace, error) {
	ns, extras, err := p.parseKnown(argv)
	if err == nil && len(extras) > 0 {
		err = &argError{msg: "unrecognized arguments: " + strings.Join(extras, " ")}
	}
	if err == nil {
		return ns, nil
	}
	if ae, ok := err.(*argError); ok {
		return nil, &ExitError{Code: 2, Stderr: usageOf(p.cmd) + "shortcut " + p.cmd + ": error: " + ae.Error() + "\n"}
	}
	return nil, err
}

// argError is ArgumentError: with an action the message names it.
type argError struct {
	act *action
	msg string
}

func (e *argError) Error() string {
	if e.act == nil {
		return e.msg
	}
	return "argument " + e.act.name() + ": " + e.msg
}

// optTuple is what _parse_optional finds for one word: the action (nil if the parser has no such option), the option
// string it matched, and the explicit argument (`=value`, or the rest of a short word) when there is one.
type optTuple struct {
	act      *action
	flag     string
	sep      string
	explicit *string
}

var negativeNumberRe = regexp.MustCompile(`^-\.?\d`)

func (p *parser) parseOptional(arg string) []optTuple {
	if arg == "" || arg[0] != '-' {
		return nil
	}
	if a := p.byFlag[arg]; a != nil {
		return []optTuple{{act: a, flag: arg}}
	}
	if len(arg) == 1 {
		return nil
	}
	if f, rest, ok := strings.Cut(arg, "="); ok && p.byFlag[f] != nil {
		return []optTuple{{act: p.byFlag[f], flag: f, sep: "=", explicit: &rest}}
	}
	if tuples := p.optionTuples(arg); len(tuples) > 0 {
		return tuples
	}
	if negativeNumberRe.MatchString(arg) { // no option of any command looks like a number
		return nil
	}
	if strings.Contains(arg, " ") {
		return nil
	}
	return []optTuple{{flag: arg}}
}

// optionTuples is _get_option_tuples: the options a word could be the start of.
func (p *parser) optionTuples(arg string) []optTuple {
	var out []optTuple
	if arg[1] == '-' {
		prefix, rest, ok := strings.Cut(arg, "=")
		for _, f := range p.flagOrder {
			if strings.HasPrefix(f, prefix) {
				t := optTuple{act: p.byFlag[f], flag: f}
				if ok {
					t.sep, t.explicit = "=", &rest
				}
				out = append(out, t)
			}
		}
		return out
	}
	prefix, rest, ok := strings.Cut(arg, "=")
	short, shortRest := arg[:2], arg[2:]
	for _, f := range p.flagOrder {
		switch {
		case f == short:
			out = append(out, optTuple{act: p.byFlag[f], flag: f, sep: "", explicit: &shortRest})
		case strings.HasPrefix(f, prefix):
			t := optTuple{act: p.byFlag[f], flag: f}
			if ok {
				t.sep, t.explicit = "=", &rest
			}
			out = append(out, t)
		}
	}
	return out
}

// nargsPattern is _get_nargs_pattern of a positional (A is a word, - is a `--`).
func nargsPattern(a *action) string {
	switch a.nargs {
	case '?':
		return "(-*A?-*)"
	case '*':
		return "(-*[A-]*)"
	case '+':
		return "(-*A[A-]*)"
	}
	return "(-*A-*)"
}

// optionCount is _match_argument for an option: how many of the words that follow it (as a pattern) it takes.
func optionCount(a *action, pattern string) (int, error) {
	switch a.kind {
	case kTrue, kHelp:
		return 0, nil
	case kOptional:
		if strings.HasPrefix(pattern, "A") {
			return 1, nil
		}
		return 0, nil
	}
	if strings.HasPrefix(pattern, "A") {
		return 1, nil
	}
	return 0, &argError{act: a, msg: "expected one argument"}
}

// matchPartial is _match_arguments_partial: how many words each of the leading positionals takes.
func matchPartial(actions []*action, pattern string) []int {
	for i := len(actions); i > 0; i-- {
		var re strings.Builder
		re.WriteString("^")
		for _, a := range actions[:i] {
			re.WriteString(nargsPattern(a))
		}
		m := regexp.MustCompile(re.String()).FindStringSubmatchIndex(pattern)
		if m == nil {
			continue
		}
		counts := make([]int, i)
		for g := range counts {
			counts[g] = m[2*g+3] - m[2*g+2]
		}
		if end := m[1]; end < len(pattern) && pattern[end] == 'O' {
			for len(counts) > 0 && counts[len(counts)-1] == 0 {
				counts = counts[:len(counts)-1]
			}
		}
		return counts
	}
	return nil
}

func (p *parser) defaults() Namespace {
	ns := Namespace{}
	for _, a := range p.actions {
		switch {
		case a.kind == kHelp && a.flags != nil:
		case a.flags == nil:
			ns[a.dest] = a.def
		case a.kind == kTrue:
			ns[a.dest] = false
		case a.kind == kAppend:
			ns[a.dest] = []string{}
		default:
			ns[a.dest] = nil
		}
	}
	return ns
}

// parseKnown is _parse_known_args: the namespace and the words nothing took.
func (p *parser) parseKnown(argv []string) (Namespace, []string, error) {
	ns := p.defaults()
	optAt := map[int][]optTuple{}
	last := -1
	var pat strings.Builder
	for i := 0; i < len(argv); i++ {
		if argv[i] == "--" {
			pat.WriteByte('-')
			for i++; i < len(argv); i++ {
				pat.WriteByte('A')
			}
			break
		}
		if t := p.parseOptional(argv[i]); t != nil {
			optAt[i], last = t, i
			pat.WriteByte('O')
		} else {
			pat.WriteByte('A')
		}
	}
	pattern := pat.String()

	seen := map[*action]bool{}
	seenNonDefault := map[*action]bool{}
	var extras []string

	takeAction := func(a *action, words []string) error {
		seen[a] = true
		var val any
		switch {
		case len(words) == 0 && a.nargs == '?' && a.flags == nil:
			val = a.def
		case len(words) == 0 && a.kind == kOptional:
			val = a.konst
		case len(words) == 0 && a.nargs == '*' && a.flags == nil:
			val = []string{}
			if s, ok := a.def.([]string); ok {
				val = s
			}
		case len(words) == 1 && (a.flags == nil && (a.nargs == 0 || a.nargs == '?') || a.flags != nil && a.kind != kTrue):
			if err := checkChoice(a, words[0]); err != nil {
				return err
			}
			val = words[0]
		default:
			for _, w := range words {
				if err := checkChoice(a, w); err != nil {
					return err
				}
			}
			val = append([]string{}, words...)
		}
		if a.flags != nil || len(words) > 0 {
			seenNonDefault[a] = true
			for _, c := range p.conflicts[a] {
				if seenNonDefault[c] {
					return &argError{act: a, msg: "not allowed with argument " + c.name()}
				}
			}
		}
		switch a.kind {
		case kHelp:
			if a.flags != nil {
				return &ExitError{Code: 0, Stdout: helpText(p.cmd)}
			}
		case kTrue:
			ns[a.dest] = true
		case kAppend:
			ns[a.dest] = append(append([]string{}, ns.List(a.dest)...), val.(string))
		default:
			ns[a.dest] = val
		}
		return nil
	}

	consumeOptional := func(start int) (int, error) {
		tuples := optAt[start]
		if len(tuples) > 1 {
			names := make([]string, len(tuples))
			for i, t := range tuples {
				names[i] = t.flag
			}
			return 0, &argError{msg: "ambiguous option: " + argv[start] + " could match " + strings.Join(names, ", ")}
		}
		t := tuples[0]
		act, flagName, sep, explicit := t.act, t.flag, t.sep, t.explicit
		type taken struct {
			a     *action
			words []string
		}
		var todo []taken
		stop := 0
		for {
			if act == nil {
				extras = append(extras, argv[start])
				return start + 1, nil
			}
			if explicit != nil {
				n, _ := optionCount(act, "A")
				switch {
				case n == 0 && flagName[1] != '-' && *explicit != "":
					if sep != "" || (*explicit)[0] == '-' {
						return 0, &argError{act: act, msg: "ignored explicit argument " + pyRepr(*explicit)}
					}
					todo = append(todo, taken{act, nil})
					r, size := utf8.DecodeRuneInString(*explicit)
					if size == 0 || (r == utf8.RuneError && size == 1) {
						size = 1
					}
					flagName = "-" + (*explicit)[:size]
					rest := (*explicit)[size:]
					if next := p.byFlag[flagName]; next != nil {
						act, explicit = next, &rest
						switch {
						case rest == "":
							sep, explicit = "", nil
						case rest[0] == '=':
							trim := rest[1:]
							sep, explicit = "=", &trim
						default:
							sep = ""
						}
					} else {
						extras = append(extras, "-"+*explicit)
						stop = start + 1
						goto done
					}
				case n == 1:
					todo = append(todo, taken{act, []string{*explicit}})
					stop = start + 1
					goto done
				default:
					return 0, &argError{act: act, msg: "ignored explicit argument " + pyRepr(*explicit)}
				}
			} else {
				n, err := optionCount(act, pattern[start+1:])
				if err != nil {
					return 0, err
				}
				todo = append(todo, taken{act, argv[start+1 : start+1+n]})
				stop = start + 1 + n
				goto done
			}
		}
	done:
		for _, tk := range todo {
			if err := takeAction(tk.a, tk.words); err != nil {
				return 0, err
			}
		}
		return stop, nil
	}

	var positionals []*action
	for _, a := range p.actions {
		if a.flags == nil {
			positionals = append(positionals, a)
		}
	}
	consumePositionals := func(start int) (int, error) {
		counts := matchPartial(positionals, pattern[start:])
		for i, n := range counts {
			words := append([]string{}, argv[start:start+n]...)
			if strings.Contains(pattern[start:start+n], "-") {
				for j, w := range words {
					if w == "--" {
						words = append(words[:j], words[j+1:]...)
						break
					}
				}
			}
			start += n
			if err := takeAction(positionals[i], words); err != nil {
				return 0, err
			}
		}
		positionals = positionals[len(counts):]
		return start, nil
	}

	start := 0
	for start <= last {
		next := start
		for next <= last && optAt[next] == nil {
			next++
		}
		if start != next {
			end, err := consumePositionals(start)
			if err != nil {
				return nil, nil, err
			}
			if end > start {
				start = end
				continue
			}
		}
		if optAt[start] == nil {
			extras = append(extras, argv[start:next]...)
			start = next
		}
		var err error
		if start, err = consumeOptional(start); err != nil {
			return nil, nil, err
		}
	}
	stop, err := consumePositionals(start)
	if err != nil {
		return nil, nil, err
	}
	extras = append(extras, argv[stop:]...)

	var missing []string
	for _, a := range p.actions {
		if !seen[a] && a.flags == nil && (a.nargs == 0 || a.nargs == '+') {
			missing = append(missing, a.name())
		}
	}
	if len(missing) > 0 {
		return nil, nil, &argError{msg: "the following arguments are required: " + strings.Join(missing, ", ")}
	}
	return ns, extras, nil
}

func checkChoice(a *action, v string) error {
	if a.choices == nil {
		return nil
	}
	quoted := make([]string, len(a.choices))
	for i, c := range a.choices {
		if c == v {
			return nil
		}
		quoted[i] = pyRepr(c)
	}
	return &argError{act: a, msg: fmt.Sprintf("invalid choice: %s (choose from %s)", pyRepr(v), strings.Join(quoted, ", "))}
}

// pyRepr is repr() of a str. A byte that is not UTF-8 is what Python's surrogateescape makes of it, \udcXX.
func pyRepr(s string) string {
	quote := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte(quote)
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, "\\udc%02x", s[i])
		case r == '\\':
			b.WriteString("\\\\")
		case r == rune(quote):
			b.WriteString("\\" + string(r))
		case r == '\n':
			b.WriteString("\\n")
		case r == '\r':
			b.WriteString("\\r")
		case r == '\t':
			b.WriteString("\\t")
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\x%02x", r)
		case r < 0x7f || unicode.IsPrint(r):
			b.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&b, "\\x%02x", r)
		case r < 0x10000:
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			fmt.Fprintf(&b, "\\U%08x", r)
		}
		i += n
	}
	b.WriteByte(quote)
	return b.String()
}
