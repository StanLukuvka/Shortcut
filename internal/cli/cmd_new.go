package cli

import (
	"regexp"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/term"
	"github.com/StanLukuvka/Shortcut/internal/term/argparse"
)

// positionalAnywhere matches $1, $@, $* or ${1} anywhere in a command.
var positionalAnywhere = regexp.MustCompile(`\$(?:[1-9@*]|\{[1-9@*])`)

// cmdNew makes a shortcut from an inline command after -- or --script.
func cmdNew(c *store.Context, s *term.Streams, cmd term.Command, args []string) error {
	// everything after the first -- is the command, and argparse never sees it
	pre, inline, hasInline := args, []string(nil), false
	if i := slices.Index(args, "--"); i >= 0 {
		pre, inline, hasInline = args[:i], args[i+1:], true
	}
	ns, err := cmd.Parse(pre)
	if err != nil {
		return err
	}
	script := ns.Str("script")
	switch {
	case hasInline && len(inline) == 0:
		return term.Die("command expected after '--'")
	case ns.Set("script") && script == "":
		return term.Die("--script needs a file name")
	case ns.Set("desc") && ns.Str("desc") == "":
		return term.Die("--desc can't be empty")
	case ns.Set("namespace") && ns.Str("namespace") == "":
		return term.Die("--namespace can't be empty")
	case hasInline && script != "":
		return term.Die("use one of: '-- CMD', --script")
	case !hasInline && script == "":
		return argparse.UsageError("new", "give a command after '--', or --script PATH")
	}

	if c.CwdErr != nil {
		return c.CwdErr
	}
	group, err := targetGroup(c, ns)
	if err != nil {
		return err
	}
	name := ns.Str("name")
	fields, err := entryFields(c, name, ns, group)
	if err != nil {
		return err
	}
	if script != "" {
		return newFromScript(c, s, ns, group, fields)
	}
	text := joinCommand(inline)
	entry := *fields
	entry.Inline = &text
	entry.Description = descOr(ns, text)
	if err := register(c, s, group, &entry); err != nil {
		return err
	}
	if !positionalAnywhere.MatchString(text) {
		s.Err.WriteString("note: '" + name + "' won't see any arguments you give it; add \"$@\" to pass them on\n")
	}
	_, err = s.Out.WriteString("Created inline shortcut '" + group.Display(name) + "'.\n")
	return err
}

// descOr returns the --desc, or def when there is none.
func descOr(ns term.Namespace, def string) string {
	if ns.Set("desc") {
		return ns.Str("desc")
	}
	return def
}

// entryFields returns the parts of an entry the flags of new set.
func entryFields(c *store.Context, name string, ns term.Namespace, g store.Group) (*store.Entry, error) {
	var cwd *string
	if ns.Set("cwd") {
		v := ns.Str("cwd")
		var err error
		if cwd, err = storedCwd(c, &v, g); err != nil {
			return nil, err
		}
	}
	e := &store.Entry{Name: name, Cwd: cwd, Confirm: ns.Bool("confirm")}
	return e, nil
}

// newFromScript makes an entry that names the file where it is. A file inside the folder its paths
// resolve from (the project, the namespace file's folder, the global store) is kept relative, so the folder can
// move.
func newFromScript(c *store.Context, s *term.Streams, ns term.Namespace, group store.Group, fields *store.Entry) error {
	path := ns.Str("script")
	abs := platform.Abs(platform.ExpandUser(path))
	if path == "-" || !platform.IsFile(abs) {
		return term.Die("script file not found: " + path)
	}
	base := platform.Realpath(group.ScriptBase())
	real := platform.Realpath(abs)
	field := abs
	if platform.IsRelativeTo(real, base) {
		field = platform.Relpath(real, base)
	}
	entry := *fields
	entry.Script = &field
	entry.Description = descOr(ns, "(no description)")
	if err := register(c, s, group, &entry); err != nil {
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
			quoted[i] = platform.ShellQuote(word)
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
