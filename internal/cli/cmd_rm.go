package cli

import (
	"strconv"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/cli/argparse"
	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// What rm deletes: the entry's line in its registry, never a script file; for rm --namespace the namespace's file
// and its scripts folder. Nothing else, and nothing that is only reached through a link: unlinkFile never follows
// one, and a symlinked scripts folder loses only the link.

// cmdRm removes a shortcut or a namespace.
func cmdRm(c *store.Context, s *textio.Streams, args []string) error {
	ns, err := parse("rm", args)
	if err != nil {
		return err
	}
	names := ns.List("name")
	if ns.Set("namespace") {
		if len(names) > 0 {
			return Die("--namespace takes the namespace alone: shortcut rm --namespace " + ns.Str("namespace") +
				". To remove one entry: shortcut rm " + ns.Str("namespace") + " " + platform.ShellJoin(names))
		}
		if ns.Bool("local") {
			return Die("--namespace takes the namespace alone: shortcut rm --namespace " + ns.Str("namespace"))
		}
		return rmNamespace(c, s, ns.Str("namespace"), ns.Bool("yes"))
	}
	if len(names) == 0 {
		return argparse.UsageError("rm", "missing name")
	}
	if ns.Bool("local") {
		return rmLocal(c, s, ns)
	}

	// A project that can't be read might hide the name, so rm doesn't reach past it to a global one
	root := ""
	if !ns.Bool("skipLocal") {
		root = c.Project
	}
	if root != "" {
		if broken := c.Store(root).Main.Error; broken != nil {
			return Die(*broken + ". Inside this project rm only takes --local until it's fixed; " +
				"for your own: shortcut rm --global " + platform.ShellJoin(names))
		}
	}

	// Raw lookup: an entry that can't run must still be removable
	found, err := store.FindEntry(c, names, store.Lookup{GlobalOnly: ns.Bool("skipLocal")})
	if err != nil {
		return err
	}
	if len(found.Rest) > 0 {
		return store.TooManyNames(names)
	}
	group, name := found.Group, found.Name
	display := group.Display(name)
	if group.InProject {
		return Die("'" + display + "' is a project shortcut, and shortcut doesn't change " + store.ProjectFile + ". " +
			"Edit it by hand, or use: shortcut rm --local " + display)
	}

	if err := confirmed(s, textio.Confirm{Question: "remove '" + display + "'?", NoTerminal: "remove '" + display + "' in a terminal, or pass -y",
		Message: rmMessage(group, display, found.Item), TellOnYes: true}, ns.Bool("yes")); err != nil {
		return err
	}

	err = lockedGroupWrite(c, group, func() error {
		reg, shortcuts, err := loadForEdit(group.File, false)
		if err != nil {
			return err
		}
		if shortcuts.Has(name) {
			shortcuts.Delete(name)
			if err := saveEdited(group.File, reg, shortcuts); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.Out.WriteString("Removed shortcut '" + display + "'.\n")
	noteIfUnapproved(c, s, group)
	return nil
}

// rmLocal takes a name out of .shortcuts.local, which also undoes an override.
func rmLocal(c *store.Context, s *textio.Streams, ns Namespace) error {
	if ns.Bool("skipLocal") {
		return Die("--local and --global can't be used together")
	}
	names := ns.List("name")
	if len(names) > 1 {
		return Die("--local takes one name: namespaces aren't in " + store.LocalFile)
	}
	name := names[0]
	root, err := c.LocalProjectRoot()
	if err != nil {
		return err
	}
	group := c.Store(root).Main
	if _, inLocal := group.Local.Get(name); !inLocal {
		if _, inShared := group.Shared.Get(name); inShared {
			return Die("'" + name + "' isn't in " + store.LocalFile + ", and shortcut doesn't change " + store.ProjectFile)
		}
		return Die("'" + name + "' isn't in " + store.ProjectFile + " or " + store.LocalFile + " here")
	}
	if err := confirmed(s, textio.Confirm{Question: "remove '" + name + "'?", NoTerminal: "remove '" + name + "' in a terminal, or pass -y",
		Message: rmLocalMessage(group, name), TellOnYes: true}, ns.Bool("yes")); err != nil {
		return err
	}
	after, err := writeLocal(c, group.StoreDir, func(shortcuts *platform.Doc) error {
		shortcuts.Delete(name)
		return nil
	})
	if err != nil {
		return err
	}
	noteIfUnapproved(c, s, after)
	s.Out.WriteString("Removed '" + name + "' from " + store.LocalFile + ".\n")
	return nil
}

// rmNamespace removes a global namespace, whole. One in imports/ goes with its scripts folder. One that
// shortcuts.json imports from elsewhere is only no longer imported: its files are someone's own.
func rmNamespace(c *store.Context, s *textio.Streams, name string, assumeYes bool) error {
	storeDir := c.Global
	st := c.Store(storeDir)
	group, found := st.Namespace(name)
	if !found {
		return Die("no namespace named '" + name + "' in your global store (" + platform.Join(storeDir, "imports") + ")")
	}
	listed := isListedImport(st, name)
	folder := group.ScriptsDir()
	var lines []string
	if listed {
		lines = []string{"Stops importing '" + name + "' in " + st.Main.File, "Keeps its file: " + group.File}
	} else {
		lines = []string{"Removes namespace '" + name + "' (" + strconv.Itoa(len(group.Shortcuts())) + " shortcuts): " + group.File}
		if platform.Lexists(folder) {
			lines = append(lines, "Deletes its scripts folder: "+folder)
		}
	}
	if err := confirmed(s, textio.Confirm{Question: "remove namespace '" + name + "'?", NoTerminal: "remove namespace '" + name + "' in a terminal, or pass -y",
		Message: strings.Join(lines, "\n"), TellOnYes: true}, assumeYes); err != nil {
		return err
	}
	approvalFile := c.ApprovalPath(group) // its key needs the file, so before it goes
	err := c.WithRegistryLock(storeDir, func() error {
		if listed {
			reg, err := store.LoadRegistry(storeDir)
			if err != nil {
				return err
			}
			if imports, ok := reg.Object("imports"); ok {
				imports.Delete(name)
				if len(imports.Keys) == 0 {
					reg.Delete("imports")
				} else {
					raw, err := imports.Bytes()
					if err != nil {
						return err
					}
					reg.Set("imports", raw)
				}
			}
			if err := store.SaveRegistryAtomic(storeDir, reg); err != nil {
				return err
			}
		} else if err := removeNamespaceFiles(group); err != nil {
			return err
		}
		return unlinkFile(approvalFile)
	})
	if err != nil {
		return err
	}
	s.Out.WriteString("Removed namespace '" + name + "'.\n")
	return nil
}

// keptScript returns the script an entry names, as a path, for saying it stays. "" is none.
func keptScript(group store.Group, it store.Item) string {
	if it.Entry == nil || it.Entry.Script == nil || *it.Entry.Script == "" {
		return ""
	}
	script := *it.Entry.Script
	if path, err := store.ResolveScriptPath(group.ScriptBase(), script); err == nil {
		return path
	}
	return script
}

// rmMessage returns what rm is about to change, shown above its question.
func rmMessage(group store.Group, display string, it store.Item) string {
	where := "your global shortcuts"
	if group.Kind == store.KindNamespace {
		where = "namespace " + group.Name
	}
	lines := []string{"Removes '" + display + "' from " + where + ": " + group.File}
	if kept := keptScript(group, it); kept != "" {
		lines = append(lines, "Keeps its script: "+kept)
	}
	return strings.Join(lines, "\n")
}

// rmLocalMessage returns what rm --local is about to change in .shortcuts.local, shown above its question.
func rmLocalMessage(group store.Group, name string) string {
	mine, _ := group.Local.Get(name)
	lines := []string{"Removes your '" + name + "' from " + group.LocalFile + "."}
	if _, shared := group.Shared.Get(name); shared {
		lines = append(lines, "The '"+name+"' in "+store.ProjectFile+" applies again.")
	}
	if kept := keptScript(group, mine); kept != "" {
		lines = append(lines, "Keeps its script: "+kept)
	}
	return strings.Join(lines, "\n")
}
