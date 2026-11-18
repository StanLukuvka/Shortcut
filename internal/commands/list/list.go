package list

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/terminal"
)

// Run lists shortcuts.
func Run(c *store.Context, s *terminal.Streams, cmd terminal.Command, args []string) error {
	ns, err := cmd.Parse(args)
	if err != nil {
		return err
	}
	stores := c.Stores()
	if ns.Set("names") { // one scope: the project's with ".", else the global store's
		if ns.Str("names") == "." {
			stores = stores[:len(stores)-1]
		} else {
			stores = stores[len(stores)-1:]
		}
	}
	if c.ProjectErr != nil && (!ns.Set("names") || ns.Str("names") == ".") { // the project is in this listing
		s.Err.WriteString("warning: " + c.ProjectErr.Error() + "\n")
	}
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

	if names := ns.Str("names"); names != "" && names != "." {
		for _, st := range stores {
			if group, isNS := st.Namespace(names); isNS && usable(group) {
				s.Out.WriteString(strings.Join(slices.Sorted(slices.Values(group.Shortcuts().Names())), "\n") + "\n")
			}
		}
		return nil
	}

	if ns.Set("names") {
		var names []string
		for _, st := range stores {
			names = append(names, slices.Sorted(slices.Values(st.Main.Shortcuts().Names()))...)
			for _, n := range st.Namespaces {
				if usable(n.Group) {
					names = append(names, n.Name)
				}
			}
		}
		for _, n := range names {
			s.Out.WriteString(n + "\n")
		}
		return nil
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
		if sections > 0 {
			s.Out.WriteString("\n")
		}
		sections++
		s.Out.WriteString(title + ":\n")
		if d, _ := g.Doc.String("description"); d != "" { // ok ignored: a description that isn't a string makes the group unusable (fileError), and only usable groups get here
			s.Out.WriteString("  " + d + "\n")
		}
		type row struct{ name, mode, source, desc string }
		var rows []row
		items := slices.SortedFunc(slices.Values(g.Shortcuts()), func(a, b store.Item) int { return strings.Compare(a.Name, b.Name) })
		for _, it := range items {
			name := it.Name
			desc := it.Text("description")
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
			line := "  " + fmt.Sprintf("%-20s", r.name) + " " + fmt.Sprintf("%-*s", width, r.mode) + "  "
			if r.source != "" {
				line += fmt.Sprintf("%-*s", sourceWidth, r.source) + "  "
			}
			s.Out.WriteString(line + r.desc + "\n")
		}
	}

	for _, st := range stores {
		if st.Main.Kind == store.KindProject {
			if len(st.Main.Shortcuts()) > 0 {
				section("Project shortcuts ("+st.Main.Label()+"), run with: shortcut r . <name>", st.Main, "")
			}
		} else if len(st.Main.Shortcuts()) > 0 {
			section("Global shortcuts", st.Main, "")
		}
		for _, n := range st.Namespaces {
			if usable(n.Group) {
				reach := ""
				if n.Group.InProject {
					reach = ", run with: shortcut r . " + n.Name + " <name>"
				}
				section("Namespace "+n.Name+" ("+n.Group.File+")"+reach, n.Group, n.Name+" ")
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
