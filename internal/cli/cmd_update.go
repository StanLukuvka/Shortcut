package cli

import (
	"maps"
	"slices"

	"github.com/StanLukuvka/Shortcut/internal/approval"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// cmdUpdate reviews changes to project shortcuts and namespaces, and approve them.
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
	globalNames := globalNamesOf(stores)
	code := 0
	named := ns.Str("group") != ""
	for _, g := range groups {
		rc, err := approveGroup(s, g, ns.Bool("yes"), globalNames, named)
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

// approveGroup shows what changed since the last approval and asks. What is shown is what gets
// hashed. globalNames are your global shortcuts and namespaces, which a project's names hide inside it. It returns
// the exit code; an error is a die (no terminal to ask on, or the record can't be written). A broken group
// is skipped with a warning (exit 0) unless the group was named, then it is an error (exit 1).
func approveGroup(s *textio.Streams, g model.Group, assumeYes bool, globalNames map[string]bool, named bool) (int, error) {
	prefix, rc := "warning: skipped ", 0
	if named {
		prefix, rc = "error: cannot approve ", 1
	}
	if g.Error != nil {
		s.Err.WriteString(prefix + g.Label() + ": " + *g.Error + "\n")
		return rc, nil
	}
	if err := approval.Check(g); err != nil {
		s.Err.WriteString("error: " + err.Error() + "\n")
		return 1, nil
	}
	if errs := model.EntryErrors(g); len(errs) > 0 {
		for _, e := range errs {
			s.Err.WriteString(prefix + g.Label() + ": " + e + "\n")
		}
		return rc, nil
	}
	shared, err := approval.SharedFiles(g)
	if err != nil {
		s.Err.WriteString("error: " + err.Error() + "\n")
		return 1, nil
	}
	local, err := approval.LocalFiles(g)
	if err != nil {
		s.Err.WriteString("error: " + err.Error() + "\n")
		return 1, nil
	}
	files := maps.Clone(shared)
	maps.Copy(files, local)
	if approval.State(g) == "ok" {
		s.Out.WriteString(g.Label() + ": already approved, nothing has changed.\n")
		return 0, nil
	}
	old := map[string]string{}
	if record := approval.Load(g); record != nil {
		maps.Copy(old, record.Snapshot)
		maps.Copy(old, record.Local)
	}
	where := ""
	if g.Name != "" {
		where = " (" + pyos.Realpath(g.File) + ")" // the real place, wherever it points
	}
	head := "New to you:"
	if len(old) > 0 {
		head = "Changes to"
	}
	s.Out.WriteString(head + " " + g.Label() + where + "\n")
	textio.ShowDiff(s, approval.DiffFiles(old, files))
	local2 := g.LocalShortcuts()
	for _, name := range slices.Sorted(slices.Values(g.SharedShortcuts().Keys)) {
		if _, both := local2.Get(name); both {
			s.Out.WriteString("'" + name + "' in " + model.ProjectFile + " is " + g.Source(name) + " by " + model.LocalFile + " for you.\n")
		}
	}
	shortcuts := g.Shortcuts()
	if g.IsProject() {
		hidden := shortcuts.Keys
		if g.Name != "" {
			hidden = []string{g.Name}
		}
		for _, name := range slices.Sorted(slices.Values(hidden)) {
			if globalNames[name] {
				s.Out.WriteString("Inside this project, '" + name + "' hides your global '" + name + "'.\n")
			}
		}
	}
	for _, name := range slices.Sorted(slices.Values(shortcuts.Keys)) {
		data, ok := shortcuts.Vals[name].(*pyjson.Object)
		if !ok {
			continue
		}
		cwd, isStr := data.Vals["cwd"].(string)
		if exported, _ := data.Vals["export"].(bool); exported && isStr && model.IsRelativeCwd(&cwd) {
			s.Err.WriteString("warning: '" + name + "' is exported, but its cwd '" + cwd + "' only exists in this checkout\n")
		}
	}
	granted, err := approval.ConfirmReview("approve", old, files, "approve "+g.Label()+" in a terminal, or pass -y").Granted(s, assumeYes)
	if err != nil {
		return 0, err
	}
	if !granted {
		return 1, nil
	}
	if err := approval.Save(g, shared, local); err != nil {
		return 0, err
	}
	covers := "its definitions"
	if g.Name != "" {
		covers = "its definitions and its scripts folder"
	}
	s.Out.WriteString("Approved " + g.Label() + ". This covers " + covers + ", not what they call. " +
		"Any change to them needs `" + g.UpdateCmd() + "` again.\n")
	return 0, nil
}
