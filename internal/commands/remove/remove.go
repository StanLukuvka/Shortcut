package remove

import (
	"strconv"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/store/write"
	"github.com/StanLukuvka/Shortcut/internal/terminal"
	"github.com/StanLukuvka/Shortcut/internal/terminal/argparse"
)

// What rm deletes: the entry's line in its registry, never a script file; for rm --namespace the namespace's file
// and its scripts folder. Nothing else, and nothing that is only reached through a link: write.UnlinkFile never follows
// one, and a symlinked scripts folder loses only the link.

// Run removes a shortcut or a namespace.
func Run(c *store.Context, s *terminal.Streams, cmd terminal.Command, args []string) error {
	ns, err := cmd.Parse(args)
	if err != nil {
		return err
	}
	names := ns.List("name")
	if ns.Set("namespace") {
		if len(names) > 0 || ns.Bool("local") {
			return terminal.Die("--namespace takes the namespace alone: shortcut rm --namespace " + ns.Str("namespace"))
		}
		return rmNamespace(c, s, ns.Str("namespace"), ns.Bool("yes"))
	}
	if len(names) == 0 {
		return argparse.UsageError("rm", "missing name")
	}
	if ns.Bool("local") {
		return rmLocal(c, s, ns)
	}

	// Raw lookup: an entry that can't run must still be removable
	found, err := store.FindEntry(c, names, store.Lookup{Cmd: "rm"})
	if err != nil {
		return err
	}
	if len(found.Rest) > 0 {
		return store.TooManyNames(names)
	}
	group, name := found.Group, found.Name
	display := group.Display(name)

	if err := terminal.Confirmed(s, terminal.Confirm{Question: "remove '" + display + "'?", NoTerminal: "remove '" + display + "' in a terminal, or pass -y",
		Message: rmMessage(group, display), TellOnYes: true}, ns.Bool("yes")); err != nil {
		return err
	}

	err = c.WithLock(func() error {
		reg, shortcuts, err := write.LoadForEdit(group.File, false)
		if err != nil {
			return err
		}
		if !shortcuts.Has(name) {
			return terminal.Die("'" + display + "' is no longer in " + group.File + "; nothing was removed")
		}
		delete(shortcuts, name)
		return write.SaveEdited(group.File, reg, shortcuts)
	})
	if err != nil {
		return err
	}
	s.Out.WriteString("Removed shortcut '" + display + "'.\n")
	return nil
}

// rmLocal takes a name out of .shortcuts.local, which also undoes an override.
func rmLocal(c *store.Context, s *terminal.Streams, ns argparse.Namespace) error {
	names := ns.List("name")
	if len(names) > 1 {
		return terminal.Die("--local takes one name: namespaces aren't in " + store.LocalFile)
	}
	name := names[0]
	root, err := c.LocalProjectRoot()
	if err != nil {
		return err
	}
	group := c.Store(root).Main
	if _, inLocal := group.Local.Get(name); !inLocal {
		if _, inShared := group.Shared.Get(name); inShared {
			return terminal.Die("'" + name + "' isn't in " + store.LocalFile + ", and shortcut doesn't change " + store.ProjectFile)
		}
		return terminal.Die("'" + name + "' isn't in " + store.ProjectFile + " or " + store.LocalFile + " here")
	}
	if err := terminal.Confirmed(s, terminal.Confirm{Question: "remove '" + name + "'?", NoTerminal: "remove '" + name + "' in a terminal, or pass -y",
		Message: "Removes your '" + name + "' from " + group.LocalFile + ".", TellOnYes: true}, ns.Bool("yes")); err != nil {
		return err
	}
	err = write.WriteLocal(c, group.StoreDir, func(shortcuts platform.Doc) error {
		delete(shortcuts, name)
		return nil
	})
	if err != nil {
		return err
	}
	s.Out.WriteString("Removed '" + name + "' from " + store.LocalFile + ".\n")
	return nil
}

// rmNamespace removes a global namespace, whole. One in imports/ goes with its scripts folder. One that
// shortcuts.json imports from elsewhere is only no longer imported: its files are someone's own.
func rmNamespace(c *store.Context, s *terminal.Streams, name string, assumeYes bool) error {
	storeDir := c.Global
	st := c.Store(storeDir)
	group, found := st.Namespace(name)
	if !found {
		return terminal.Die("no namespace named '" + name + "' in your global store (" + platform.Join(storeDir, "imports") + ")")
	}
	imports, _ := st.Main.Doc.Object("imports")
	listed := imports.Has(name)
	folder := group.ScriptsDir()
	var lines []string
	if listed {
		lines = []string{"Stops importing '" + name + "' in " + st.Main.File}
	} else {
		lines = []string{"Removes namespace '" + name + "' (" + strconv.Itoa(len(group.Shortcuts())) + " shortcuts): " + group.File,
			"Deletes its scripts folder, if it has one: " + folder}
	}
	if err := terminal.Confirmed(s, terminal.Confirm{Question: "remove namespace '" + name + "'?", NoTerminal: "remove namespace '" + name + "' in a terminal, or pass -y",
		Message: strings.Join(lines, "\n"), TellOnYes: true}, assumeYes); err != nil {
		return err
	}
	err := c.WithLock(func() error {
		if listed {
			reg, err := store.LoadRegistryFile(st.Main.File)
			if err != nil {
				return err
			}
			if imports, ok := reg.Object("imports"); ok {
				delete(imports, name)
				if len(imports) == 0 {
					delete(reg, "imports")
				} else {
					raw, err := imports.Bytes()
					if err != nil {
						return err
					}
					reg["imports"] = raw
				}
			}
			if err := store.SaveRegistryFile(st.Main.File, reg); err != nil {
				return err
			}
		} else if err := write.RemoveNamespaceFiles(group); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.Out.WriteString("Removed namespace '" + name + "'.\n")
	return nil
}

// rmMessage returns what rm is about to change, shown above its question.
func rmMessage(group store.Group, display string) string {
	where := "your global shortcuts"
	if group.Kind == store.KindNamespace {
		where = "namespace " + group.Name
	}
	return "Removes '" + display + "' from " + where + ": " + group.File
}
