package cli

import (
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// cmdLocation prints the path of a shortcut's script, bare.
func cmdLocation(s *textio.Streams, args []string) error {
	ns, err := parsers["location"].Parse(args)
	if err != nil {
		return err
	}
	resolved, err := model.ResolveName(ns.List("name"), ns.Bool("skipLocal"))
	if err != nil {
		return err
	}
	entry := resolved.Entry
	if entry.Inline != nil {
		return Die("'" + resolved.Display() + "' is an inline shortcut, so it has no file. Change it in the file `shortcut show` names.")
	}
	path, err := model.ResolveScriptPath(resolved.Group.ScriptBase(), *entry.Script)
	if err != nil {
		return err
	}
	_, err = s.Out.WriteString(pyos.Abs(path) + "\n")
	return err
}
