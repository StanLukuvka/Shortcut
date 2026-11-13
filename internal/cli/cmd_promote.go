package cli

import (
	"github.com/StanLukuvka/Shortcut/internal/approval"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// cmdPromote makes a project shortcut one of yours. Nothing is copied: the new entry points at
// this checkout's script (absolute) and, for a relative cwd, at its directory, so it runs the files as they are.
func cmdPromote(s *textio.Streams, args []string) error {
	ns, err := parsers["promote"].Parse(args)
	if err != nil {
		return err
	}
	callerCwd, err := pyos.Getwd()
	if err != nil {
		return err
	}
	if model.FindProjectRoot(callerCwd) == "" {
		return Die("promote runs inside a project: there's no " + model.ProjectFile + " or " + model.LocalFile + " here or above")
	}
	name, asGiven := ns.Str("name"), ns.Set("newName")
	newName := name
	if v := ns.Str("newName"); v != "" { // `parsed.new_name or parsed.name`: an empty --as is no --as
		newName = v
	}
	if err := model.ValidateName(newName); err != nil {
		return err
	}
	resolved, _, err := model.ResolveShortcut([]string{name}, callerCwd, false, true)
	if err != nil {
		return err
	}
	group, entry := resolved.Group, resolved.Entry
	if !group.IsProject() || group.Name != "" {
		return Die("'" + name + "' isn't a project shortcut here")
	}
	if err := approval.Require(group); err != nil {
		return err
	}
	if !entry.Export && !ns.Bool("force") {
		return Die("'" + name + "' is marked project-only; promote anyway with --force")
	}

	home := model.LoadStore(model.GlobalStoreDir())
	if home.Main.Error != nil {
		return Die(*home.Main.Error)
	}
	_, clash := home.Main.Shortcuts().Get(newName)
	if _, isNs := home.Namespace(newName); clash || isNs {
		if !asGiven {
			return Die("you already have a global '" + name + "'; give this one another name: shortcut promote " + name + " --as NEW")
		}
		return Die("you already have a global '" + newName + "'")
	}

	fields := entry.ToObject()
	fields.Delete("export") // a global entry can't export
	if entry.Script != nil {
		path, err := model.ResolveScriptPath(group.ScriptBase(), *entry.Script)
		if err != nil {
			return err
		}
		fields.Set("script", path)
	}
	if model.IsRelativeCwd(entry.Cwd) {
		fields.Set("cwd", pyos.Realpath(model.ResolveRunDir(entry.Cwd, group, callerCwd)))
	}
	from := &pyjson.Object{}
	from.Set("source", pyos.Realpath(group.StoreDir))
	from.Set("name", name)
	fields.Set("from", from)
	newEntry, err := model.EntryFromValue(newName, fields)
	if err != nil {
		return err
	}

	shown := &pyjson.Object{}
	shown.Set(newName, fields)
	text, err := pyjson.MarshalIndent(shown)
	if err != nil {
		return err
	}
	s.Out.WriteString("From " + group.DefiningFile(name) + ", as your global '" + newName + "':\n")
	s.Out.WriteString(string(text) + "\n")
	s.Out.WriteString("This runs the project's files as they are now, like typing the command there. " +
		"It isn't reviewed again when they change.\n")
	if err := confirmed(s, textio.Confirm{Question: "add '" + newName + "' to " + home.Main.File + "?",
		NoTerminal: "promote '" + name + "' in a terminal, or pass -y"}, ns.Bool("yes")); err != nil {
		return err
	}

	if err := register(s, home.Main, newEntry, nil); err != nil {
		return err
	}
	s.Out.WriteString("Promoted '" + name + "' to your global shortcuts as '" + newName + "'.\n")
	if newName == name {
		s.Err.WriteString("note: inside this project '" + name + "' is still the project's; shortcut r --global " + name + " runs yours\n")
	}
	return nil
}
