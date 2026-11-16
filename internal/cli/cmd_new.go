package cli

import (
	"regexp"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/cli/argparse"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/ordjson"
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
	if !positionalAnywhere.MatchString(text) && !strings.Contains(text, "SC_ARG_") {
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

// entryFields returns the parts of an entry the flags of new set, and the --arg/--arg-desc rules.
func entryFields(name string, ns Namespace, g model.Group) (*model.Entry, error) {
	var args []*ordjson.Object
	if flags := ns.List("arg"); len(flags) > 0 {
		args = []*ordjson.Object{}
		list := make([]ordjson.Value, 0, len(flags))
		for _, flag := range flags {
			argName, def, hasDef := strings.Cut(flag, "=")
			a := &ordjson.Object{}
			a.Set("name", strings.TrimSuffix(argName, "..."))
			rest := strings.HasSuffix(argName, "...")
			if rest {
				a.Set("rest", true)
			}
			if !hasDef {
				a.Set("required", true)
			} else if def != "" || !rest { // NAME...= is optional with nothing in its place
				a.Set("default", def)
			}
			args = append(args, a)
			list = append(list, a)
		}
		if err := model.ValidateArgs(name, list); err != nil {
			return nil, err
		}
	}
	for _, flag := range ns.List("argDesc") {
		argName, text, hasText := strings.Cut(flag, "=")
		var match *ordjson.Object
		for _, a := range args {
			if a.Vals["name"] == strings.TrimSuffix(argName, "...") {
				match = a
				break
			}
		}
		if !hasText || match == nil {
			return nil, Die("--arg-desc " + flag + ": needs NAME=TEXT, with NAME declared by --arg")
		}
		if _, has := match.Get("desc"); has {
			return nil, Die("--arg-desc " + flag + ": '" + match.Vals["name"].(string) + "' already has a desc")
		}
		match.Set("desc", text)
	}
	var cwd *string
	if ns.Set("cwd") {
		v := ns.Str("cwd")
		var err error
		if cwd, err = storedCwd(&v, g); err != nil {
			return nil, err
		}
	}
	e := &model.Entry{Name: name, Cwd: cwd, Confirm: ns.Bool("confirm"), Args: args}
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
