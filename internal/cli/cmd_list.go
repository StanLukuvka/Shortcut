package cli

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/approval"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// pyTruthy is Python's bool() of a JSON value.
func pyTruthy(v pyjson.Value) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case *big.Int:
		return x.Sign() != 0
	case float64:
		return x != 0
	case []pyjson.Value:
		return len(x) > 0
	case *pyjson.Object:
		return len(x.Keys) > 0
	}
	return true
}

// pyStr is Python's str() of a JSON value: a string as it is, the rest as repr shows it.
func pyStr(v pyjson.Value) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return x
	case *big.Int:
		return x.String()
	case float64:
		return pyjson.ReprFloat(x)
	case []pyjson.Value:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = reprOf(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *pyjson.Object:
		parts := make([]string, len(x.Keys))
		for i, k := range x.Keys {
			parts[i] = pyRepr(k) + ": " + reprOf(x.Vals[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(v)
}

func reprOf(v pyjson.Value) string {
	if str, ok := v.(string); ok {
		return pyRepr(str)
	}
	return pyStr(v)
}

// padRight is f"{s:<n}": padded to n code points, never cut.
func padRight(s string, n int) string {
	if w := utf8.RuneCountInString(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

func sortedNames(o *pyjson.Object) []string {
	names := append([]string(nil), o.Keys...)
	sort.Slice(names, func(i, j int) bool { return pyjson.Less(names[i], names[j]) })
	return names
}

func approved(g model.Group) bool { return approval.State(g) == "ok" }

// cmdList is cmd_list.
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
			if g.Error != nil && *g.Error != "" {
				s.Err.WriteString("warning: " + *g.Error + "\n")
			}
		}
	}

	if names := ns.Str("names"); names != "" {
		for _, store := range stores {
			group, isNS := store.Namespace(names)
			if isNS && (group.Error == nil || *group.Error == "") && approved(group) {
				s.Out.WriteString(strings.Join(sortedNames(group.Shortcuts()), "\n") + "\n")
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
				for _, n := range sortedNames(store.Main.Shortcuts()) {
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

	globalNames := map[string]bool{}
	home := stores[len(stores)-1]
	for _, k := range home.Main.Shortcuts().Keys {
		globalNames[k] = true
	}
	for _, n := range home.Namespaces {
		globalNames[n.Name] = true
	}
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
			verb := "changed since you approved them"
			if state == "new" {
				verb = "aren't approved yet"
			}
			s.Out.WriteString(title + " " + verb + ". Review them with: " + g.UpdateCmd() + "\n")
			return
		}
		s.Out.WriteString(title + ":\n")
		if d := g.Data.Vals["description"]; pyTruthy(d) {
			s.Out.WriteString("  " + pyStr(d) + "\n")
		}
		type row struct{ name, mode, source, desc string }
		var rows []row
		shared, runnable := g.SharedShortcuts(), g.Shortcuts()
		merged := &pyjson.Object{}
		for _, k := range shared.Keys {
			merged.Set(k, shared.Vals[k])
		}
		for _, k := range runnable.Keys {
			merged.Set(k, runnable.Vals[k])
		}
		for _, name := range sortedNames(merged) {
			data, _ := merged.Vals[name].(*pyjson.Object)
			if data == nil {
				data = &pyjson.Object{}
			}
			desc := ""
			if d, ok := data.Get("description"); ok {
				desc = pyStr(d)
			}
			if globalNames[name] && g.NeedsApproval() && g.Name == "" {
				desc += "  [hides global]"
			} else if localNames[name] && !g.NeedsApproval() {
				desc += "  [hidden here by project]"
			}
			mode := model.Caller
			if c := data.Vals["cwd"]; pyTruthy(c) {
				mode = pyStr(c)
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

// usable is "not group.error": the file can be used.
func usable(g model.Group) bool { return g.Error == nil || *g.Error == "" }
