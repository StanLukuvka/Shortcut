package cli

import (
	"regexp"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/ordjson"
	"github.com/StanLukuvka/Shortcut/internal/osutil"
)

func oneLine(text string) string { return strings.ReplaceAll(text, "\n", "\\n") }

func argLabel(a *ordjson.Object) string {
	label, _ := a.Vals["name"].(string)
	if a.Vals["rest"] == true {
		label += "..."
	}
	return label
}

// usageLine is the usage line: shortcut r <display> <args>.
func usageLine(display string, spec []*ordjson.Object) string {
	parts := []string{"shortcut r", display}
	for _, a := range spec {
		if a.Vals["required"] == true {
			parts = append(parts, "<"+argLabel(a)+">")
		} else {
			parts = append(parts, "["+argLabel(a)+"]")
		}
	}
	return strings.Join(parts, " ")
}

// argLines returns one line per declared argument, its name, whether it's needed or its default, and its desc.
func argLines(spec []*ordjson.Object) []string {
	type row struct{ label, need, desc string }
	rows := make([]row, len(spec))
	width, needWidth := 0, 0
	for i, a := range spec {
		need := "optional"
		if a.Vals["required"] == true {
			need = "required"
		} else if d, ok := a.Vals["default"].(string); ok {
			need = "default: " + oneLine(osutil.ShellQuote(d))
		}
		desc, _ := a.Vals["desc"].(string)
		rows[i] = row{argLabel(a), need, oneLine(desc)}
		width = max(width, len([]rune(rows[i].label)))
		needWidth = max(needWidth, len([]rune(need)))
	}
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = strings.TrimRight("  "+padRight(r.label, width)+"  "+padRight(r.need, needWidth)+"  "+r.desc, " \t\n")
	}
	return lines
}

func isFlag(token string) bool { return len(token) > 1 && strings.HasPrefix(token, "-") }

// bindArgs checks presence and count against the declared args, and fills defaults. It returns the words the
// shortcut gets and its SC_ARG_* variables as "K=V". --flags are never parsed: they don't count and stay where they
// are. A first -- is dropped, and every word after it counts, so a value can start with a dash. A failure is the
// exit 2 of fail(): the message, the usage line and, if any argument has a desc, the list.
func bindArgs(display string, spec []*ordjson.Object, argv []string) ([]string, []string, error) {
	fail := func(msg string) error {
		text := "error: " + msg + "\nusage: " + usageLine(display, spec) + "\n"
		for _, a := range spec {
			if d, _ := a.Vals["desc"].(string); d != "" {
				text += strings.Join(argLines(spec), "\n") + "\n"
				break
			}
		}
		return &ExitError{Code: 2, Stderr: text}
	}

	end := len(argv)
	for i, t := range argv {
		if t == "--" {
			end = i
			break
		}
	}
	if end < len(argv) {
		argv = append(append([]string(nil), argv[:end]...), argv[end+1:]...)
	}
	counts := make([]bool, len(argv))
	var positional []string
	last := -1
	for i, t := range argv {
		if counts[i] = i >= end || !isFlag(t); counts[i] {
			positional = append(positional, t)
			last = i
		}
	}
	hasRest := len(spec) > 0 && spec[len(spec)-1].Vals["rest"] == true
	if len(positional) > len(spec) && !hasRest {
		return nil, nil, fail("too many arguments")
	}
	var env, fill []string
	for i, a := range spec {
		isRest := hasRest && i == len(spec)-1
		var values []string
		if i < len(positional) {
			values = positional[i:min(i+1, len(positional))]
			if isRest {
				values = positional[i:]
			}
		}
		name, _ := a.Vals["name"].(string)
		if len(values) == 0 {
			if d, ok := a.Vals["default"].(string); ok {
				values = []string{d}
				fill = append(fill, d)
			} else if a.Vals["required"] == true {
				return nil, nil, fail("missing <" + name + ">")
			} else {
				fill = append(fill, "") // keeps a later default at its own position
			}
		} else if a.Vals["required"] == true && strings.Join(values, "") == "" {
			return nil, nil, fail("<" + name + "> can't be empty")
		}
		value := strings.Join(values, "")
		if isRest {
			value = osutil.ShellJoin(values)
		}
		env = append(env, model.ArgVar(name)+"="+value)
	}
	for len(fill) > 0 && fill[len(fill)-1] == "" {
		fill = fill[:len(fill)-1]
	}
	out := append(append(append([]string(nil), argv[:last+1]...), fill...), argv[last+1:]...)
	return out, env, nil
}

var positionalAt = regexp.MustCompile(`^\$(?:[1-9@*]|\{[1-9@*])`) // $1, $@, $*, ${1}: how a shell command reads its arguments

// joinCommand turns `-- cmd word word` into one inline command. Each word stays one word, as typed, and
// only the shortcut's own arguments stay live, because $1 is why someone quoted it past their shell. A lookahead
// for POSITIONAL at each $ is a check in code, since Go's regexp has none.
func joinCommand(words []string) string {
	if len(words) == 1 {
		return words[0]
	}
	quoted := make([]string, len(words))
	for i, word := range words {
		live := false
		for j := 0; j < len(word); j++ {
			if positionalAt.MatchString(word[j:]) {
				live = true
				break
			}
		}
		if !live {
			quoted[i] = osutil.ShellQuote(word)
			continue
		}
		var b strings.Builder
		b.WriteByte('"')
		for j := 0; j < len(word); j++ {
			c := word[j]
			if c == '"' || c == '\\' || c == '`' || (c == '$' && !positionalAt.MatchString(word[j:])) {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		}
		b.WriteByte('"')
		quoted[i] = b.String()
	}
	return strings.Join(quoted, " ")
}
