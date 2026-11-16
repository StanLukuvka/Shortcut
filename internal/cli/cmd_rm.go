package cli

import (
	"strconv"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/cli/argparse"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/ordjson"
	"github.com/StanLukuvka/Shortcut/internal/osutil"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// What rm deletes: the entry's line in its registry, never a script file; for rm --namespace the namespace's file
// and its scripts folder. Nothing else, and nothing that is only reached through a link: unlinkFile never follows
// one, and a symlinked scripts folder loses only the link.

// cmdRm removes a shortcut or a namespace.
func cmdRm(s *textio.Streams, args []string) error {
	ns, err := parsers["rm"].Parse(args)
	if err != nil {
		return err
	}
	names := ns.List("name")
	if ns.Set("namespace") {
		if len(names) > 0 {
			return Die("--namespace takes the namespace alone: shortcut rm --namespace " + ns.Str("namespace") +
				". To remove one entry: shortcut rm " + ns.Str("namespace") + " " + osutil.ShellJoin(names))
		}
		if ns.Bool("local") {
			return Die("--namespace takes the namespace alone: shortcut rm --namespace " + ns.Str("namespace"))
		}
		return rmNamespace(s, ns.Str("namespace"), ns.Bool("yes"))
	}
	if len(names) == 0 {
		return argparse.UsageError("rm", "missing name")
	}
	if ns.Bool("local") {
		return rmLocal(s, ns)
	}

	// A project that can't be read might hide the name, so rm doesn't reach past it to a global one
	cwd, _ := osutil.Getwd()
	root := ""
	if !ns.Bool("skipLocal") {
		root = model.FindProjectRoot(cwd)
	}
	if root != "" {
		if broken := model.LoadProjectGroup(root).Error; broken != nil {
			return Die(*broken + ". Inside this project rm only takes --local until it's fixed; " +
				"for your own: shortcut rm --global " + osutil.ShellJoin(names))
		}
	}

	// Raw lookup: an entry that can't run must still be removable
	found, err := model.FindEntry(names, cwd, ns.Bool("skipLocal"), false)
	if err != nil {
		return err
	}
	if len(found.Rest) > 0 {
		return model.TooManyNames(names)
	}
	group, name := found.Group, found.Name
	display := group.Display(name)
	if group.IsProject() {
		return Die("'" + display + "' is a project shortcut, and shortcut doesn't change " + model.ProjectFile + ". " +
			"Edit it by hand, or use: shortcut rm --local " + display)
	}

	if err := confirmed(s, textio.Confirm{Question: "remove '" + display + "'?", NoTerminal: "remove '" + display + "' in a terminal, or pass -y",
		Message: rmMessage(group, display, found.Raw), TellOnYes: true}, ns.Bool("yes")); err != nil {
		return err
	}

	err = lockedGroupWrite(group, func() error {
		reg, shortcuts, err := loadForEdit(group.File, false)
		if err != nil {
			return err
		}
		if _, has := shortcuts.Get(name); has {
			shortcuts.Delete(name)
			if err := model.SaveRegistryFile(group.File, reg); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.Out.WriteString("Removed shortcut '" + display + "'.\n")
	noteIfUnapproved(s, group)
	return nil
}

// rmLocal takes a name out of .shortcuts.local, which also undoes an override.
func rmLocal(s *textio.Streams, ns Namespace) error {
	if ns.Bool("skipLocal") {
		return Die("--local and --global can't be used together")
	}
	names := ns.List("name")
	if len(names) > 1 {
		return Die("--local takes one name: namespaces aren't in " + model.LocalFile)
	}
	name := names[0]
	cwd, _ := osutil.Getwd()
	root, err := model.LocalProjectRoot(cwd)
	if err != nil {
		return err
	}
	group := model.LoadStore(root).Main
	if _, inLocal := group.LocalShortcuts().Get(name); !inLocal {
		if _, inShared := group.SharedShortcuts().Get(name); inShared {
			return Die("'" + name + "' isn't in " + model.LocalFile + ", and shortcut doesn't change " + model.ProjectFile)
		}
		return Die("'" + name + "' isn't in " + model.ProjectFile + " or " + model.LocalFile + " here")
	}
	if err := confirmed(s, textio.Confirm{Question: "remove '" + name + "'?", NoTerminal: "remove '" + name + "' in a terminal, or pass -y",
		Message: rmLocalMessage(group, name), TellOnYes: true}, ns.Bool("yes")); err != nil {
		return err
	}
	after, err := writeLocal(group.StoreDir, func(shortcuts *ordjson.Object) error {
		shortcuts.Delete(name)
		return nil
	})
	if err != nil {
		return err
	}
	noteIfUnapproved(s, after)
	s.Out.WriteString("Removed '" + name + "' from " + model.LocalFile + ".\n")
	return nil
}

// rmNamespace removes a global namespace, whole. One in imports/ goes with its scripts folder. One that
// shortcuts.json imports from elsewhere is only no longer imported: its files are someone's own.
func rmNamespace(s *textio.Streams, name string, assumeYes bool) error {
	storeDir := model.GlobalStoreDir()
	store := model.LoadStore(storeDir)
	group, found := store.Namespace(name)
	if !found {
		return Die("no namespace named '" + name + "' in your global store (" + osutil.Join(storeDir, "imports") + ")")
	}
	listed := isListedImport(store, name)
	folder := group.ScriptsDir()
	var lines []string
	if listed {
		lines = []string{"Stops importing '" + name + "' in " + store.Main.File, "Keeps its file: " + group.File}
	} else {
		lines = []string{"Removes namespace '" + name + "' (" + strconv.Itoa(len(group.Shortcuts().Keys)) + " shortcuts): " + group.File}
		if osutil.Lexists(folder) {
			lines = append(lines, "Deletes its scripts folder: "+folder)
		}
	}
	if err := confirmed(s, textio.Confirm{Question: "remove namespace '" + name + "'?", NoTerminal: "remove namespace '" + name + "' in a terminal, or pass -y",
		Message: strings.Join(lines, "\n"), TellOnYes: true}, assumeYes); err != nil {
		return err
	}
	approvalFile := model.ApprovalPath(group) // its key needs the file, so before it goes
	err := model.WithRegistryLock(storeDir, func() error {
		if listed {
			reg, err := model.LoadRegistry(storeDir)
			if err != nil {
				return err
			}
			if imports, ok := reg.Vals["imports"].(*ordjson.Object); ok {
				imports.Delete(name)
				if len(imports.Keys) == 0 {
					reg.Delete("imports")
				}
			}
			if err := model.SaveRegistryAtomic(storeDir, reg); err != nil {
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

// scriptField is the entry's "script" when it is a non-empty string.
func scriptField(raw ordjson.Value) (string, bool) {
	entry, ok := raw.(*ordjson.Object)
	if !ok {
		return "", false
	}
	v, _ := entry.Get("script")
	script, ok := v.(string)
	return script, ok && script != ""
}

// keptScript returns the script an entry names, as a path, for saying it stays. "" is none.
func keptScript(group model.Group, raw ordjson.Value) string {
	script, ok := scriptField(raw)
	if !ok {
		return ""
	}
	if path, err := model.ResolveScriptPath(group.ScriptBase(), script); err == nil {
		return path
	}
	return script
}

// rmMessage returns what rm is about to change, shown above its question.
func rmMessage(group model.Group, display string, raw ordjson.Value) string {
	where := "your global shortcuts"
	if group.Name != "" {
		where = "namespace " + group.Name
	}
	lines := []string{"Removes '" + display + "' from " + where + ": " + group.File}
	if kept := keptScript(group, raw); kept != "" {
		lines = append(lines, "Keeps its script: "+kept)
	}
	return strings.Join(lines, "\n")
}

// rmLocalMessage returns what rm --local is about to change in .shortcuts.local, shown above its question.
func rmLocalMessage(group model.Group, name string) string {
	mine, _ := group.LocalShortcuts().Get(name)
	lines := []string{"Removes your '" + name + "' from " + group.LocalFile + "."}
	if _, shared := group.SharedShortcuts().Get(name); shared {
		lines = append(lines, "The '"+name+"' in "+model.ProjectFile+" applies again.")
	}
	if kept := keptScript(group, mine); kept != "" {
		lines = append(lines, "Keeps its script: "+kept)
	}
	return strings.Join(lines, "\n")
}
