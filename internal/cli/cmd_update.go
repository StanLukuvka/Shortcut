// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-3). Don't hand-edit without checking the plan.

package cli

import (
	"github.com/StanLukuvka/Shortcut/internal/approval"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// cmdUpdate is cmd_update: review changes to project shortcuts and namespaces, and approve them.
func cmdUpdate(s *textio.Streams, args []string) error {
	ns, err := parsers["update"].Parse(args)
	if err != nil {
		return err
	}
	stores := model.VisibleStores("", false)
	var groups []model.Group
	for _, store := range stores {
		for _, g := range store.Groups() {
			if g.NeedsApproval() {
				groups = append(groups, g)
			}
		}
	}
	keep := func(want func(model.Group) bool) {
		var kept []model.Group
		for _, g := range groups {
			if want(g) {
				kept = append(kept, g)
			}
		}
		groups = kept
	}
	switch group := ns.Str("group"); {
	case group == ".":
		keep(func(g model.Group) bool { return g.Name == "" })
		if len(groups) == 0 {
			return Die("no project shortcuts here: no " + model.ProjectFile + " or " + model.LocalFile + " in this folder or its parents")
		}
	case group != "":
		keep(func(g model.Group) bool { return g.Name == group })
		if len(groups) == 0 {
			return Die("no namespace named '" + group + "' here")
		}
		groups = groups[:1]
	default:
		keep(func(g model.Group) bool {
			return g.Error != nil || len(g.Shortcuts().Keys) > 0 || approval.Load(g) != nil
		})
		keep(func(g model.Group) bool { return g.Error != nil || approval.State(g) != "ok" })
		if len(groups) == 0 {
			s.Out.WriteString("Nothing to approve here.\n")
			return nil
		}
	}
	home := stores[len(stores)-1]
	globalNames := map[string]bool{}
	for _, name := range home.Main.Shortcuts().Keys {
		globalNames[name] = true
	}
	for _, n := range home.Namespaces {
		globalNames[n.Name] = true
	}
	code := 0
	for _, g := range groups {
		rc, err := approval.ApproveGroup(s, g, ns.Bool("yes"), globalNames)
		if err != nil {
			return err
		}
		code = max(code, rc)
	}
	if code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}
