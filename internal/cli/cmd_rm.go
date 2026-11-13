package cli

import (
	"strconv"
	"strings"
	"syscall"

	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// What rm deletes: the entry's line in its registry, and for rm the script copy shortcut itself made in the group's
// scripts folder; for rm --namespace the namespace's file and its scripts folder. Nothing else, and nothing that
// is only reached through a link: unlinkFile never follows one, a symlinked scripts folder loses only the link, and
// a copy is only deleted when its fully resolved path is inside the resolved scripts folder.

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
				". To remove one entry: shortcut rm " + ns.Str("namespace") + " " + pyos.ShellJoin(names))
		}
		if ns.Bool("local") {
			return Die("--namespace takes the namespace alone: shortcut rm --namespace " + ns.Str("namespace"))
		}
		return rmNamespace(s, ns.Str("namespace"), ns.Bool("yes"))
	}
	if len(names) == 0 {
		return &ExitError{Code: 2, Stderr: usageOf("rm") + "shortcut rm: error: the following arguments are required: name\n"}
	}
	if ns.Bool("local") {
		return rmLocal(s, ns)
	}

	// A project that can't be read might hide the name, so rm doesn't reach past it to a global one
	cwd, _ := pyos.Getwd()
	root := ""
	if !ns.Bool("skip_local") {
		root = model.FindProjectRoot(cwd)
	}
	if root != "" {
		if broken := model.LoadProjectGroup(root).Error; broken != nil {
			return Die(*broken + ". Inside this project rm only takes --local until it's fixed; " +
				"for your own: shortcut rm --global " + pyos.ShellJoin(names))
		}
	}

	// Raw lookup: an entry that can't run must still be removable
	found, err := model.FindEntry(names, cwd, ns.Bool("skip_local"), false)
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

	cp := storedCopy(group, found.Raw)
	if err := confirmed(s, textio.Confirm{Question: "remove '" + display + "'?", NoTerminal: "remove '" + display + "' in a terminal, or pass -y",
		Message: rmMessage(group, display, found.Raw, cp), TellOnYes: true}, ns.Bool("yes")); err != nil {
		return err
	}

	err = lockedGroupWrite(group, func() error {
		// 1. Remove from registry
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
		// 2. Remove the stored copy, never a --script file
		if cp != "" {
			syscall.Unlink(cp) // Python's suppress(OSError)
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

// rmLocal takes a name out of .shortcuts.local, which also undoes an override or a hide. A name only in
// .shortcuts is hidden instead.
func rmLocal(s *textio.Streams, ns Namespace) error {
	if ns.Bool("skip_local") {
		return Die("--local and --global can't be used together")
	}
	names := ns.List("name")
	if len(names) > 1 {
		return Die("--local takes one name: namespaces aren't in " + model.LocalFile)
	}
	name := names[0]
	cwd, _ := pyos.Getwd()
	root, err := model.LocalProjectRoot(cwd)
	if err != nil {
		return err
	}
	group := model.LoadStore(root).Main
	unreadable := model.LoadGroup(group.File, group.StoreDir, "").Error // .shortcuts can't be read: hide the name anyway
	_, inLocal := group.LocalShortcuts().Get(name)
	_, inShared := group.SharedShortcuts().Get(name)
	if !inLocal && !inShared && unreadable == nil {
		return Die("'" + name + "' isn't in " + model.ProjectFile + " or " + model.LocalFile + " here")
	}
	verb := "hide"
	if inLocal {
		verb = "remove"
	}
	if err := confirmed(s, textio.Confirm{Question: verb + " '" + name + "'?", NoTerminal: verb + " '" + name + "' in a terminal, or pass -y",
		Message: rmLocalMessage(group, name, unreadable), TellOnYes: true}, ns.Bool("yes")); err != nil {
		return err
	}
	after, err := writeLocal(group.StoreDir, func(shortcuts *pyjson.Object) error {
		if _, has := shortcuts.Get(name); has {
			shortcuts.Delete(name)
		} else {
			shortcuts.Set(name, nil)
		}
		return nil
	})
	if err != nil {
		return err
	}
	noteIfUnapproved(s, after)
	if verb == "hide" {
		s.Out.WriteString("Hid shortcut '" + name + "' from you.\n")
	} else {
		s.Out.WriteString("Removed '" + name + "' from " + model.LocalFile + ".\n")
	}
	return nil
}

// rmNamespace removes a global namespace, whole. One in imports/ goes with its scripts folder. One that
// shortcuts.json imports from elsewhere is only no longer imported: its files are someone's own.
func rmNamespace(s *textio.Streams, name string, assumeYes bool) error {
	storeDir := model.GlobalStoreDir()
	store := model.LoadStore(storeDir)
	group, found := store.Namespace(name)
	if !found {
		return Die("no namespace named '" + name + "' in your global store (" + pyos.Join(storeDir, "imports") + ")")
	}
	listed := isListedImport(store, name)
	folder := group.ScriptsDir()
	var lines []string
	if listed {
		lines = []string{"Stops importing '" + name + "' in " + store.Main.File, "Keeps its file: " + group.File}
	} else {
		lines = []string{"Removes namespace '" + name + "' (" + strconv.Itoa(len(group.Shortcuts().Keys)) + " shortcuts): " + group.File}
		if pyos.Lexists(folder) {
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
			if imports, ok := reg.Vals["imports"].(*pyjson.Object); ok {
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
func scriptField(raw pyjson.Value) (string, bool) {
	entry, ok := raw.(*pyjson.Object)
	if !ok {
		return "", false
	}
	v, _ := entry.Get("script")
	script, ok := v.(string)
	return script, ok && script != ""
}

// storedCopy returns the copy shortcut keeps of an entry's script, which rm deletes with it: a relative path
// inside the group's scripts folder. new --script never records one there as relative, so it's never one. "" is none.
// The path is resolved, so it is where the file really is, and it must still be inside the resolved folder.
func storedCopy(group model.Group, raw pyjson.Value) string {
	script, ok := scriptField(raw)
	if !ok || strings.HasPrefix(script, "/") {
		return ""
	}
	path, err := model.ResolveScriptPath(group.ScriptBase(), script)
	if err != nil || !pyos.IsRelativeTo(path, pyos.Realpath(group.ScriptsDir())) {
		return ""
	}
	return path
}

// keptScript returns the script an entry names, as a path, for saying it stays. "" is none.
func keptScript(group model.Group, raw pyjson.Value) string {
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
func rmMessage(group model.Group, display string, raw pyjson.Value, cp string) string {
	where := "your global shortcuts"
	if group.Name != "" {
		where = "namespace " + group.Name
	}
	lines := []string{"Removes '" + display + "' from " + where + ": " + group.File}
	if cp != "" {
		lines = append(lines, "Deletes its script copy: "+cp)
	} else if kept := keptScript(group, raw); kept != "" {
		lines = append(lines, "Keeps its script: "+kept)
	}
	return strings.Join(lines, "\n")
}

// rmLocalMessage returns what rm --local is about to change in .shortcuts.local, shown above its question.
func rmLocalMessage(group model.Group, name string, unreadable *string) string {
	local := group.LocalFile
	var lines []string
	if unreadable != nil {
		lines = append(lines, *unreadable+". "+model.ProjectFile+" stays as it is.")
	}
	mine, inLocal := group.LocalShortcuts().Get(name)
	switch {
	case !inLocal:
		lines = append(lines, "Hides '"+name+"' from you: writes \""+name+"\": null to "+local+".")
	case mine == nil:
		lines = append(lines, "Stops hiding '"+name+"': takes its null out of "+local+". The one in "+model.ProjectFile+" applies again.")
	default:
		lines = append(lines, "Removes your '"+name+"' from "+local+".")
		if _, shared := group.SharedShortcuts().Get(name); shared {
			lines = append(lines, "The '"+name+"' in "+model.ProjectFile+" applies again.")
		}
		if kept := keptScript(group, mine); kept != "" {
			lines = append(lines, "Keeps its script: "+kept)
		}
	}
	return strings.Join(lines, "\n")
}
