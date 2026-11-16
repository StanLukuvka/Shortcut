package update

import (
	"maps"
	"slices"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/store/approval"
	"github.com/StanLukuvka/Shortcut/internal/term"
)

// Run reviews changes to project shortcuts and namespaces, and approve them.
func Run(c *store.Context, s *term.Streams, cmd term.Command, args []string) error {
	ns, err := cmd.Parse(args)
	if err != nil {
		return err
	}
	stores := c.Visible(store.Lookup{})
	var groups []store.Group
	for _, st := range stores {
		for _, g := range st.Groups() {
			if g.NeedsApproval() {
				groups = append(groups, g)
			}
		}
	}
	keep := func(want func(store.Group) bool) {
		var kept []store.Group
		for _, g := range groups {
			if want(g) {
				kept = append(kept, g)
			}
		}
		groups = kept
	}
	switch group := ns.Str("group"); {
	case group == ".":
		keep(func(g store.Group) bool { return g.Kind == store.KindProject })
		if len(groups) == 0 {
			return term.Die("no project shortcuts here: no " + store.ProjectFile + " or " + store.LocalFile + " in this folder or its parents")
		}
	case group != "":
		keep(func(g store.Group) bool { return g.Name == group })
		if len(groups) == 0 {
			return term.Die("no namespace named '" + group + "' here")
		}
		groups = groups[:1]
	default:
		keep(func(g store.Group) bool {
			return g.Error != nil || len(g.Shortcuts()) > 0 || approval.Load(c, g) != nil
		})
		keep(func(g store.Group) bool { return g.Error != nil || approval.State(c, g) != store.StateOK })
		if len(groups) == 0 {
			s.Out.WriteString("Nothing to approve here.\n")
			return nil
		}
	}
	globalNames := store.GlobalNamesOf(stores)
	code := 0
	named := ns.Str("group") != ""
	for _, g := range groups {
		rc, err := approveGroup(c, s, g, globalNames, approveOpts{Yes: ns.Bool("yes"), Named: named})
		if err != nil {
			return err
		}
		code = max(code, rc)
	}
	if code != 0 {
		return &term.ExitError{Code: code}
	}
	return nil
}

// approveOpts is how approveGroup was asked: -y, and whether the group was named.
type approveOpts struct{ Yes, Named bool }

// approveGroup lists what is new, changed or removed since the last approval and asks. What is listed is what gets
// hashed. globalNames are your global shortcuts and namespaces, which a project's names hide inside it. It returns
// the exit code; an error is a die (no terminal to ask on, or the record can't be written). A broken group
// is skipped with a warning (exit 0) unless the group was named, then it is an error (exit 1).
func approveGroup(c *store.Context, s *term.Streams, g store.Group, globalNames map[string]bool, opts approveOpts) (int, error) {
	prefix, rc := "warning: skipped ", 0
	if opts.Named {
		prefix, rc = "error: cannot approve ", 1
	}
	if g.Error != nil {
		s.Err.WriteString(prefix + g.Label() + ": " + *g.Error + "\n")
		return rc, nil
	}
	if err := approval.Check(c, g); err != nil {
		s.Err.WriteString("error: " + err.Error() + "\n")
		return 1, nil
	}
	if errs := store.EntryErrors(g); len(errs) > 0 {
		for _, e := range errs {
			s.Err.WriteString(prefix + g.Label() + ": " + e + "\n")
		}
		return rc, nil
	}
	files, err := approval.Files(g)
	if err != nil {
		s.Err.WriteString("error: " + err.Error() + "\n")
		return 1, nil
	}
	if approval.State(c, g) == store.StateOK {
		s.Out.WriteString(g.Label() + ": already approved, nothing has changed.\n")
		return 0, nil
	}
	old := map[string]string{}
	if record := approval.Load(c, g); record != nil {
		old = record.Files
	}
	where := ""
	if g.Kind == store.KindNamespace {
		where = " (" + platform.Realpath(g.File) + ")" // the real place, wherever it points
	}
	head := "New to you:"
	if len(old) > 0 {
		head = "Changes to"
	}
	s.Out.WriteString(head + " " + g.Label() + where + "\n")
	paths := maps.Clone(old)
	maps.Copy(paths, files)
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		before, inOld := old[path]
		after, inNew := files[path]
		switch {
		case !inNew:
			s.Out.WriteString("  removed  " + path + "\n")
		case !inOld:
			s.Out.WriteString("  new      " + path + "\n")
		case before != after:
			s.Out.WriteString("  changed  " + path + "\n")
		}
	}
	local2 := g.Local
	for _, name := range slices.Sorted(slices.Values(g.Shared.Names())) {
		if _, both := local2.Get(name); both {
			s.Out.WriteString("'" + name + "' in " + store.ProjectFile + " is " + string(g.Source(name)) + " by " + store.LocalFile + " for you.\n")
		}
	}
	shortcuts := g.Shortcuts()
	if g.InProject {
		hidden := shortcuts.Names()
		if g.Kind == store.KindNamespace {
			hidden = []string{g.Name}
		}
		for _, name := range slices.Sorted(slices.Values(hidden)) {
			if globalNames[name] {
				s.Out.WriteString("Inside this project, '" + name + "' hides your global '" + name + "'.\n")
			}
		}
	}
	granted, err := term.Confirm{Question: "approve?", NoTerminal: "approve " + g.Label() + " in a terminal, or pass -y"}.Granted(s, opts.Yes)
	if err != nil {
		return 0, err
	}
	if !granted {
		return 1, nil
	}
	if err := approval.Save(c, g, files); err != nil {
		return 0, err
	}
	covers := "its definitions"
	if g.Kind == store.KindNamespace {
		covers = "its definitions and its scripts folder"
	}
	s.Out.WriteString("Approved " + g.Label() + ". This covers " + covers + ", not what they call. " +
		"Any change to them needs `" + g.UpdateCmd() + "` again.\n")
	return 0, nil
}
