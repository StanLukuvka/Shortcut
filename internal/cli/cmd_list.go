package cli

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/approval"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/ordjson"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

func approved(g model.Group) bool { return approval.State(g) == "ok" }

// cmdList lists shortcuts.
func cmdList(s *textio.Streams, args []string) error {
	ns, err := parsers["list"].Parse(args)
	if err != nil {
		return err
	}
	stores := model.VisibleStores("", false)
	for _, store := range stores {
		for _, p := range store.Problems {
			s.Err.WriteString("warning: " + p + "\n")
		}
		for _, g := range store.Groups() {
			if !usable(g) {
				s.Err.WriteString("warning: " + *g.Error + "\n")
			}
		}
	}

	if names := ns.Str("names"); names != "" {
		for _, store := range stores {
			group, isNS := store.Namespace(names)
			if isNS && usable(group) && approved(group) {
				s.Out.WriteString(strings.Join(slices.Sorted(slices.Values(group.Shortcuts().Keys)), "\n") + "\n")
			}
			if _, inMain := store.Main.Shortcuts().Get(names); isNS || inMain {
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
		for _, store := range stores {
			if approved(store.Main) {
				for _, n := range slices.Sorted(slices.Values(store.Main.Shortcuts().Keys)) {
					add(n)
				}
			}
			for _, n := range store.Namespaces {
				if usable(n.Group) && approved(n.Group) {
					add(n.Name)
				}
			}
		}
		for _, n := range names {
			s.Out.WriteString(n + "\n")
		}
		return nil
	}

	globalNames := globalNamesOf(stores)
	localNames := map[string]bool{}
	if len(stores) > 1 && approved(stores[0].Main) {
		for _, k := range stores[0].Main.Shortcuts().Keys {
			localNames[k] = true
		}
	}

	for _, store := range stores {
		for _, g := range store.Groups() {
			if usable(g) {
				for _, e := range model.EntryErrors(g) {
					s.Err.WriteString("warning: " + g.Label() + ": " + e + "\n")
				}
			}
		}
	}

	sections := 0
	section := func(title string, g model.Group, prefix string) {
		state := approval.State(g)
		if sections > 0 {
			s.Out.WriteString("\n")
		}
		sections++
		if state != "ok" {
			one := g.Name != ""
			pick := func(singular, plural string) string {
				if one {
					return singular
				}
				return plural
			}
			verb := pick("changed since you approved it", "changed since you approved them")
			if state == "new" {
				verb = pick("isn't approved yet", "aren't approved yet")
			}
			review := pick("it", "them")
			s.Out.WriteString(title + " " + verb + ". Review " + review + " with: " + g.UpdateCmd() + "\n")
			return
		}
		s.Out.WriteString(title + ":\n")
		if d := textOf(g.Data.Vals["description"]); d != "" {
			s.Out.WriteString("  " + d + "\n")
		}
		type row struct{ name, mode, source, desc string }
		var rows []row
		shared, runnable := g.SharedShortcuts(), g.Shortcuts()
		merged := &ordjson.Object{}
		for _, k := range shared.Keys {
			merged.Set(k, shared.Vals[k])
		}
		for _, k := range runnable.Keys {
			merged.Set(k, runnable.Vals[k])
		}
		for _, name := range slices.Sorted(slices.Values(merged.Keys)) {
			data, _ := merged.Vals[name].(*ordjson.Object)
			if data == nil {
				data = &ordjson.Object{}
			}
			desc := textOf(data.Vals["description"])
			if globalNames[name] && g.NeedsApproval() && g.Name == "" {
				desc += "  [hides global]"
			} else if localNames[name] && !g.NeedsApproval() {
				desc += "  [hidden here by project]"
			}
			mode := model.Caller
			if c := textOf(data.Vals["cwd"]); c != "" {
				mode = c
			}
			source := ""
			if g.LocalFile != "" {
				source = g.Source(name)
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

	for _, store := range stores {
		if store.Main.NeedsApproval() {
			if len(store.Main.SharedShortcuts().Keys) > 0 || len(store.Main.Shortcuts().Keys) > 0 {
				section("Project shortcuts ("+store.Main.Key()+")", store.Main, "")
			}
		} else if len(store.Main.Shortcuts().Keys) > 0 {
			section("Global shortcuts", store.Main, "")
		}
		for _, n := range store.Namespaces {
			if usable(n.Group) {
				hides := ""
				if n.Group.IsProject() && globalNames[n.Name] {
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

// globalNamesOf is the names your global store holds: its shortcuts and its namespaces, which a project's names hide.
func globalNamesOf(stores []model.Store) map[string]bool {
	names := map[string]bool{}
	home := stores[len(stores)-1]
	for _, k := range home.Main.Shortcuts().Keys {
		names[k] = true
	}
	for _, n := range home.Namespaces {
		names[n.Name] = true
	}
	return names
}

// usable reports whether the group's file can be used (it has no error).
func usable(g model.Group) bool { return g.Error == nil || *g.Error == "" }

// textOf is a field's text. A global store entry is not validated before it is listed, so a field that is not a string
// shows as it was written (a number as its digits) instead of failing.
func textOf(v ordjson.Value) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	return fmt.Sprint(v)
}
