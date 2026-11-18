package new

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/store/write"
	"github.com/StanLukuvka/Shortcut/internal/terminal"
	"github.com/StanLukuvka/Shortcut/internal/terminal/argparse"
)

// positionalAnywhere matches $1, $@, $* or ${1} anywhere in a command.
var positionalAnywhere = regexp.MustCompile(`\$(?:[1-9@*]|\{[1-9@*])`)

// Run makes a shortcut from an inline command after -- or --script.
func Run(c *store.Context, s *terminal.Streams, cmd terminal.Command, args []string) error {
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
		return terminal.Die("command expected after '--'")
	case ns.Set("script") && script == "":
		return terminal.Die("--script needs a file name")
	case ns.Set("desc") && ns.Str("desc") == "":
		return terminal.Die("--desc can't be empty")
	case ns.Set("namespace") && ns.Str("namespace") == "":
		return terminal.Die("--namespace can't be empty")
	case hasInline && script != "":
		return terminal.Die("use one of: '-- CMD', --script")
	case len(inline) > 1:
		return argparse.UsageError("new", "put the command after -- in single quotes, as one word: -- '"+strings.Join(inline, " ")+"'")
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
	fields := &store.Entry{Name: name, Confirm: ns.Bool("confirm")}
	if ns.Set("cwd") {
		if fields.Cwd, err = storedCwd(c, ns.Str("cwd"), group); err != nil {
			return err
		}
	}
	if script != "" {
		return newFromScript(c, s, ns, group, fields)
	}
	text := inline[0]
	entry := *fields
	entry.Inline = &text
	entry.Description = ns.Str("desc")
	if err := write.Register(c, group, &entry); err != nil {
		return err
	}
	if !positionalAnywhere.MatchString(text) {
		s.Err.WriteString("note: '" + name + "' won't see any arguments you give it; add \"$@\" to pass them on\n")
	}
	s.Out.WriteString("Created inline shortcut '" + group.Display(name) + "'.\n")
	return nil
}

// newFromScript makes an entry that names the file where it is. A file inside the folder its paths
// resolve from (the project, the namespace file's folder, the global store) is kept relative, so the folder can
// move.
func newFromScript(c *store.Context, s *terminal.Streams, ns argparse.Namespace, group store.Group, fields *store.Entry) error {
	path := ns.Str("script")
	expanded, err := platform.ExpandUser(path)
	if err != nil {
		return err
	}
	abs := platform.Join(c.Cwd, expanded) // not cleaned: `..` after a link is resolved physically
	isFile, err := platform.IsFile(abs)
	if err != nil {
		return err
	}
	if path == "-" || !isFile {
		return terminal.Die("script file not found: " + path)
	}
	if err := platform.CheckExecutable(abs); err != nil {
		return terminal.Die(err.Error())
	}
	real, err := platform.Realpath(abs)
	if err != nil {
		return err
	}
	field := abs
	// A folder the paths resolve from may not be there yet (a new namespace in a fresh store, README "Namespaces"):
	// then the script is not inside it and its path stays absolute.
	baseThere, err := platform.Exists(group.ScriptBase())
	if err != nil {
		return err
	}
	if baseThere {
		base, err := platform.Realpath(group.ScriptBase())
		if err != nil {
			return err
		}
		if platform.IsRelativeTo(real, base) {
			if field, err = filepath.Rel(base, real); err != nil {
				return err
			}
		}
	}
	entry := *fields
	entry.Script = &field
	entry.Description = ns.Str("desc")
	if err := write.Register(c, group, &entry); err != nil {
		return err
	}
	s.Out.WriteString("Created shortcut '" + group.Display(entry.Name) + "' using " + abs + ".\n")
	return nil
}

// targetGroup returns the file a new shortcut goes in. It creates nothing; write.Register does.
func targetGroup(c *store.Context, ns argparse.Namespace) (store.Group, error) {
	if err := store.ValidateName(ns.Str("name")); err != nil {
		return store.Group{}, err
	}
	local, namespace := ns.Bool("local"), ns.Str("namespace")
	if local && namespace != "" {
		return store.Group{}, terminal.Die("--namespace can't be used with --local: " + store.LocalFile + " is one file")
	}
	storeDir := c.Global
	if local {
		var err error
		if storeDir, err = c.LocalProjectRoot(); err != nil {
			return store.Group{}, err
		}
	}
	st := c.Store(storeDir)
	group := st.Main
	if namespace != "" {
		if err := store.ValidateName(namespace); err != nil {
			return store.Group{}, err
		}
		if _, clash := st.Main.Shortcuts().Get(namespace); clash {
			return store.Group{}, terminal.Die("'" + namespace + "' is a shortcut in store " + storeDir + ", so it can't be a namespace")
		}
		var ok bool
		if group, ok = st.Namespace(namespace); !ok {
			group = store.Group{Kind: store.KindNamespace, InProject: !c.IsGlobal(storeDir), Name: namespace, File: platform.Join(storeDir, "imports", namespace+store.ImportSuffix),
				StoreDir: storeDir, Doc: platform.Doc{}}
		}
	}
	if group.Error != nil {
		return store.Group{}, terminal.Die(*group.Error)
	}
	return group, nil
}

// storedCwd converts --cwd as typed (relative to the user's shell) to the 'cwd' field (relative to the
// defining file).
func storedCwd(c *store.Context, value string, g store.Group) (*string, error) {
	if value == store.Caller {
		return &value, nil
	}
	expanded, err := platform.ExpandUser(value)
	if err != nil {
		return nil, err
	}
	abs := platform.Join(c.Cwd, expanded) // not cleaned: `link/..` is the link's parent, which IsDir and Realpath find
	isDir, err := platform.IsDir(abs)
	if err != nil {
		return nil, err
	}
	if !isDir {
		return nil, terminal.Die("--cwd directory does not exist: " + abs)
	}
	path, err := platform.Realpath(abs)
	if err != nil {
		return nil, err
	}
	if g.InProject {
		store, err := platform.Realpath(g.StoreDir)
		if err != nil {
			return nil, err
		}
		if platform.IsRelativeTo(path, store) {
			folder, err := platform.Realpath(g.Folder())
			if err != nil {
				return nil, err
			}
			rel, err := filepath.Rel(folder, path)
			if err != nil {
				return nil, err
			}
			return &rel, nil
		}
	}
	return &path, nil
}
