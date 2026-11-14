package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/ordjson"
	"github.com/StanLukuvka/Shortcut/internal/osutil"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

const scriptTemplate = "#!/usr/bin/env bash\nset -euo pipefail\n\n"

// positionalAnywhere matches $1, $@, $* or ${1} anywhere in a command.
var positionalAnywhere = regexp.MustCompile(`\$(?:[1-9@*]|\{[1-9@*])`)

// cmdNew makes a shortcut from an inline command after --, a script template, --script or --copy.
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
	script, copyFrom := ns.Str("script"), ns.Str("copy")
	switch {
	case hasInline && len(inline) == 0:
		return Die("command expected after '--'")
	case (ns.Set("script") && script == "") || (ns.Set("copy") && copyFrom == ""):
		return Die("--script and --copy need a file name")
	case ns.Set("desc") && ns.Str("desc") == "":
		return Die("--desc can't be empty")
	case ns.Set("namespace") && ns.Str("namespace") == "":
		return Die("--namespace can't be empty")
	case hasInline && (script != "" || copyFrom != ""):
		return Die("use one of: '-- CMD', --script, --copy")
	case ns.Bool("local") && copyFrom != "":
		return Die("--copy can't be used with --local: " + model.LocalFile + " is one file. Use --script")
	case ns.Bool("local") && !hasInline && script == "":
		return Die("a local shortcut needs a command after '--', or --script PATH")
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
	switch {
	case script != "":
		return newFromScript(s, ns, group, fields)
	case copyFrom != "":
		return newFromCopy(s, ns, group, fields)
	case hasInline:
		text := joinCommand(inline)
		entry := *fields
		entry.Inline = &text
		entry.Description = text
		if ns.Set("desc") {
			entry.Description = ns.Str("desc")
		}
		if err := register(s, group, &entry, nil); err != nil {
			return err
		}
		if !positionalAnywhere.MatchString(text) && !strings.Contains(text, "SC_ARG_") {
			s.Err.WriteString("note: '" + name + "' won't see any arguments you give it; add \"$@\" to pass them on\n")
		}
		_, err := s.Out.WriteString("Created inline shortcut '" + group.Display(name) + "'.\n")
		return err
	}

	if _, dup := group.Shortcuts().Get(name); dup {
		return Die("shortcut '" + name + "' already exists in " + group.File)
	}
	rel, abs := storedScriptField(group, name+".sh")
	writeScript := func() error {
		if err := prepareScriptDest(group, abs); err != nil {
			return err
		}
		return writeExecutable(abs, scriptTemplate)
	}
	entry := *fields
	entry.Script = &rel
	entry.Description = "(no description)"
	if ns.Set("desc") {
		entry.Description = ns.Str("desc")
	}
	if err := register(s, group, &entry, writeScript); err != nil {
		return err
	}
	_, err = s.Out.WriteString("Created script shortcut '" + group.Display(name) + "'.\nEdit it at: " + abs + "\n")
	return err
}

// descOr returns the --desc, or def when there is none.
func descOr(ns Namespace, def string) string {
	if ns.Set("desc") {
		return ns.Str("desc")
	}
	return def
}

// refuseExisting stops new from replacing a script file no entry registers (a link counts, dangling or not).
func refuseExisting(path string) error {
	if osutil.Lexists(path) {
		return Die(path + " already exists; remove it or pick another name")
	}
	return nil
}

// writeExecutable writes the file with mode 0755, where it is, a link included.
func writeExecutable(path, content string) error {
	if err := os.WriteFile(path, []byte(content), 0o666); err != nil {
		return err
	}
	return os.Chmod(path, 0o755)
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
	if ns.Set("by") {
		by := ns.Str("by")
		e.CreatedBy = &by
	}
	today := time.Now().Format("2006-01-02")
	e.Created = &today
	return e, nil
}

// newFromScript makes an entry that names the file where it is. A file inside the folder its paths
// resolve from (the project, the namespace file's folder, the global store) is kept relative, so the folder can
// move. One in the scripts folder stays absolute: a relative path there is a copy, which rm deletes.
func newFromScript(s *textio.Streams, ns Namespace, group model.Group, fields *model.Entry) error {
	path := ns.Str("script")
	abs := osutil.Abs(osutil.ExpandUser(path))
	if path == "-" || !osutil.IsFile(abs) {
		return Die("script file not found: " + path)
	}
	base := osutil.Realpath(group.ScriptBase())
	real := osutil.Realpath(abs)
	copies := osutil.Realpath(group.ScriptsDir())
	field := abs
	if osutil.IsRelativeTo(real, base) && !osutil.IsRelativeTo(real, copies) {
		field = osutil.Relpath(real, base)
	}
	entry := *fields
	entry.Script = &field
	entry.Description = descOr(ns, "(no description)")
	if err := register(s, group, &entry, nil); err != nil {
		return err
	}
	_, err := s.Out.WriteString("Created shortcut '" + group.Display(entry.Name) + "' using " + abs + ".\n")
	return err
}

// newFromCopy keeps its own copy of the script in the store, so later changes to the source don't reach it.
func newFromCopy(s *textio.Streams, ns Namespace, group model.Group, fields *model.Entry) error {
	from := ns.Str("copy")
	var content []byte
	var src, ext string
	if from == "-" {
		if s.In == nil {
			return errors.New("stdin is closed")
		}
		var err error
		if content, err = io.ReadAll(s.In); err != nil {
			return err
		}
		ext = ".sh"
	} else {
		src = osutil.Abs(osutil.ExpandUser(from))
		if !osutil.IsFile(src) {
			return Die("script file not found: " + src)
		}
		ext = orSh(osutil.Suffix(src))
	}
	name := fields.Name
	rel, abs := storedScriptField(group, name+ext)
	writeScript := func() error {
		if err := prepareScriptDest(group, abs); err != nil {
			return err
		}
		if src == "" {
			return writeExecutable(abs, string(content))
		}
		return copyScript(src, abs)
	}
	entry := *fields
	entry.Script = &rel
	entry.Description = descOr(ns, "(no description)")
	if err := register(s, group, &entry, writeScript); err != nil {
		return err
	}
	_, err := s.Out.WriteString("Created shortcut '" + group.Display(name) + "' with a copy at " + abs + ".\n")
	return err
}

// copyScript copies the content and the source's permission bits, made executable (no setuid, setgid or sticky
// bit, no modification time). The destination is written where it is, a link included.
func copyScript(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if dstSt, err := os.Stat(dst); err == nil && os.SameFile(st, dstSt) {
		return fmt.Errorf("'%s' and '%s' are the same file", src, dst)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, st.Mode().Perm()|0o111)
}
