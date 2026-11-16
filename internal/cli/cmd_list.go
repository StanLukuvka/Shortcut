package cli

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/store/approval"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

func approved(c *store.Context, g store.Group) bool { return approval.State(c, g) == store.StateOK }

// cmdList lists shortcuts.
func cmdList(c *store.Context, s *textio.Streams, args []string) error {
	ns, err := parse("list", args)
	if err != nil {
		return err
	}
	stores := c.Visible(store.Lookup{})
	for _, st := range stores {
		for _, p := range st.Problems {
			s.Err.WriteString("warning: " + p + "\n")
		}
		for _, g := range st.Groups() {
			if !usable(g) {
				s.Err.WriteString("warning: " + *g.Error + "\n")
			}
		}
	}

	if names := ns.Str("names"); names != "" {
		for _, st := range stores {
			group, isNS := st.Namespace(names)
			if isNS && usable(group) && approved(c, group) {
				s.Out.WriteString(strings.Join(slices.Sorted(slices.Values(group.Shortcuts().Names())), "\n") + "\n")
			}
			if _, inMain := st.Main.Shortcuts().Get(names); isNS || inMain {
				break
			}
		}
		return nil
	}

	if ns.Set("names") {
		var names []string
		seen := map[string]bool{}
		add := func(n string) {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
		for _, st := range stores {
			if approved(c, st.Main) {
				for _, n := range slices.Sorted(slices.Values(st.Main.Shortcuts().Names())) {
					add(n)
				}
			}
			for _, n := range st.Namespaces {
				if usable(n.Group) && approved(c, n.Group) {
					add(n.Name)
				}
			}
		}
		for _, n := range names {
			s.Out.WriteString(n + "\n")
		}
		return nil
	}

	globalNames := store.GlobalNamesOf(stores)
	localNames := map[string]bool{}
	if len(stores) > 1 && approved(c, stores[0].Main) {
		for _, k := range stores[0].Main.Shortcuts().Names() {
			localNames[k] = true
		}
	}

	for _, st := range stores {
		for _, g := range st.Groups() {
			if usable(g) {
				for _, e := range store.EntryErrors(g) {
					s.Err.WriteString("warning: " + g.Label() + ": " + e + "\n")
				}
			}
		}
	}

	sections := 0
	section := func(title string, g store.Group, prefix string) {
		state := approval.State(c, g)
		if sections > 0 {
			s.Out.WriteString("\n")
		}
		sections++
		if state != store.StateOK {
			one := g.Kind == store.KindNamespace
			pick := func(singular, plural string) string {
				if one {
					return singular
				}
				return plural
			}
			verb := pick("changed since you approved it", "changed since you approved them")
			if state == store.StateNew {
				verb = pick("isn't approved yet", "aren't approved yet")
			}
			review := pick("it", "them")
			s.Out.WriteString(title + " " + verb + ". Review " + review + " with: " + g.UpdateCmd() + "\n")
			return
		}
		s.Out.WriteString(title + ":\n")
		if d, _ := g.Doc.String("description"); d != "" {
			s.Out.WriteString("  " + d + "\n")
		}
		type row struct{ name, mode, source, desc string }
		var rows []row
		items := slices.Clone(g.Shortcuts())
		slices.SortFunc(items, func(a, b store.Item) int { return strings.Compare(a.Name, b.Name) })
		for _, it := range items {
			name := it.Name
			desc := it.Text("description")
			if globalNames[name] && g.Kind == store.KindProject {
				desc += "  [hides global]"
			} else if localNames[name] && g.Kind == store.KindGlobal {
				desc += "  [hidden here by project]"
			}
			mode := store.Caller
			if c := it.Text("cwd"); c != "" {
				mode = c
			}
			source := ""
			if g.Kind == store.KindProject {
				source = string(g.Source(name))
			}
			rows = append(rows, row{prefix + name, mode, source, desc})
		}
		width, sourceWidth := 0, 0
		for _, r := range rows {
			width = max(width, utf8.RuneCountInString(r.mode))
			sourceWidth = max(sourceWidth, utf8.RuneCountInString(r.source))
		}
		for _, r := range rows {
			line := "  " + padRight(r.name, 20) + " " + padRight(r.mode, width) + "  "
			if r.source != "" {
				line += padRight(r.source, sourceWidth) + "  "
			}
			s.Out.WriteString(line + r.desc + "\n")
		}
	}

	for _, st := range stores {
		if st.Main.Kind == store.KindProject {
			if len(st.Main.Shortcuts()) > 0 {
				section("Project shortcuts ("+st.Main.Key()+")", st.Main, "")
			}
		} else if len(st.Main.Shortcuts()) > 0 {
			section("Global shortcuts", st.Main, "")
		}
		for _, n := range st.Namespaces {
			if usable(n.Group) {
				hides := ""
				if n.Group.InProject && globalNames[n.Name] {
					hides = "  [hides global]"
				}
				section("Namespace "+n.Name+" ("+n.Group.File+")"+hides, n.Group, n.Name+" ")
			}
		}
	}
	if sections == 0 {
		s.Err.WriteString("No shortcuts yet. Make one with: shortcut new hi -- 'echo hello \"$1\"'\n")
	}
	return nil
}

// usable reports whether the group's file can be used (it has no error).
func usable(g store.Group) bool { return g.Error == nil || *g.Error == "" }
