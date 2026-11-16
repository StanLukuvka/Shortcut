package cli

import (
	"regexp"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/cli/argparse"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/osutil"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// positionalAnywhere matches $1, $@, $* or ${1} anywhere in a command.
var positionalAnywhere = regexp.MustCompile(`\$(?:[1-9@*]|\{[1-9@*])`)

// cmdNew makes a shortcut from an inline command after -- or --script.
func cmdNew(s *textio.Streams, args []string) error {
	// everything after the first -- is the command, and argparse never sees it
	pre, inline, hasInline := args, []string(nil), false
	if i := slices.Index(args, "--"); i >= 0 {
		pre, inline, hasInline = args[:i], args[i+1:], true
	}
	ns, err := parsers["new"].Parse(pre)
	if err != nil {
		return err
	}
	script := ns.Str("script")
	switch {
	case hasInline && len(inline) == 0:
		return Die("command expected after '--'")
	case ns.Set("script") && script == "":
		return Die("--script needs a file name")
	case ns.Set("desc") && ns.Str("desc") == "":
		return Die("--desc can't be empty")
	case ns.Set("namespace") && ns.Str("namespace") == "":
		return Die("--namespace can't be empty")
	case hasInline && script != "":
		return Die("use one of: '-- CMD', --script")
	case !hasInline && script == "":
		return argparse.UsageError("new", "give a command after '--', or --script PATH")
	}

	cwd, err := osutil.Getwd()
	if err != nil {
		return err
	}
	group, err := targetGroup(ns, cwd)
	if err != nil {
		return err
	}
	name := ns.Str("name")
	fields, err := entryFields(name, ns, group)
	if err != nil {
		return err
	}
	if script != "" {
		return newFromScript(s, ns, group, fields)
	}
	text := joinCommand(inline)
	entry := *fields
	entry.Inline = &text
	entry.Description = descOr(ns, text)
	if err := register(s, group, &entry); err != nil {
		return err
	}
	if !positionalAnywhere.MatchString(text) {
		s.Err.WriteString("note: '" + name + "' won't see any arguments you give it; add \"$@\" to pass them on\n")
	}
	_, err = s.Out.WriteString("Created inline shortcut '" + group.Display(name) + "'.\n")
	return err
}

// descOr returns the --desc, or def when there is none.
func descOr(ns Namespace, def string) string {
	if ns.Set("desc") {
		return ns.Str("desc")
	}
	return def
}

// entryFields returns the parts of an entry the flags of new set.
func entryFields(name string, ns Namespace, g model.Group) (*model.Entry, error) {
	var cwd *string
	if ns.Set("cwd") {
		v := ns.Str("cwd")
		var err error
		if cwd, err = storedCwd(&v, g); err != nil {
			return nil, err
		}
	}
	e := &model.Entry{Name: name, Cwd: cwd, Confirm: ns.Bool("confirm")}
	return e, nil
}

// newFromScript makes an entry that names the file where it is. A file inside the folder its paths
// resolve from (the project, the namespace file's folder, the global store) is kept relative, so the folder can
// move.
func newFromScript(s *textio.Streams, ns Namespace, group model.Group, fields *model.Entry) error {
	path := ns.Str("script")
	abs := osutil.Abs(osutil.ExpandUser(path))
	if path == "-" || !osutil.IsFile(abs) {
		return Die("script file not found: " + path)
	}
	base := osutil.Realpath(group.ScriptBase())
	real := osutil.Realpath(abs)
	field := abs
	if osutil.IsRelativeTo(real, base) {
		field = osutil.Relpath(real, base)
	}
	entry := *fields
	entry.Script = &field
	entry.Description = descOr(ns, "(no description)")
	if err := register(s, group, &entry); err != nil {
		return err
	}
	_, err := s.Out.WriteString("Created shortcut '" + group.Display(entry.Name) + "' using " + abs + ".\n")
	return err
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
