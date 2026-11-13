// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-2). Don't hand-edit without checking the plan.

package cli

import (
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// cmdLocation is cmd_location: the path of a shortcut's script, bare.
func cmdLocation(s *textio.Streams, args []string) error {
	ns, err := parsers["location"].Parse(args)
	if err != nil {
		return err
	}
	resolved, err := model.ResolveName(ns.List("name"), ns.Bool("skip_local"))
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
