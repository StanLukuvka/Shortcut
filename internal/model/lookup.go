package model

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
)

// Found is what FindEntry finds, before anyone judges the entry.
type Found struct {
	Group Group
	Name  string
	Raw   pyjson.Value // the entry as written; LoadEntry judges it
	Rest  []string     // the words left over, a run's arguments
	Hides bool         // a project's name hides a global one of either kind
}

// FindEntry finds `name` or `namespace name` in the project, then the global store, without judging
// the entry. words must not be empty. strict (for run) refuses every name inside a project whose file can't be used,
// and a store's load errors for the name it has. Without it (show, location, rm) a broken file is looked in as far as
// it reads, so you can still see and fix what's there. both lookup failures are just errors here:
// main prints either as "error: <text>".
func FindEntry(words []string, callerCwd string, skipLocal, strict bool) (Found, error) {
	stores := VisibleStores(callerCwd, skipLocal)
	first, rest := words[0], words[1:]
	homeIdx := len(stores) - 1
	home := stores[homeIdx]

	for i, store := range stores {
		if strict && i != homeIdx && store.Main.Error != nil {
			return Found{}, fmt.Errorf("%s. Nothing runs inside this project until it's fixed; for your own: shortcut r --global %s",
				*store.Main.Error, first)
		}
		var problems []string
		if store.Main.Error != nil {
			problems = append(problems, *store.Main.Error)
		}
		problems = append(problems, store.Problems...)
		named := has(store.Main.Shortcuts(), first)
		if _, ns := store.Namespace(first); ns {
			named = true
		}
		if imports, ok := store.Main.Data.Vals["imports"].(*pyjson.Object); ok && has(imports, first) {
			named = true
		}
		if strict && len(problems) > 0 && named {
			return Found{}, errors.New(strings.Join(problems, "; "))
		}
		// A project's name, shortcut or namespace, hides a global one of either kind
		_, homeNS := home.Namespace(first)
		hides := i != homeIdx && (has(home.Main.Shortcuts(), first) || homeNS)
		shortcuts := store.Main.Shortcuts()
		_, isNS := store.Namespace(first)
		if has(shortcuts, first) && (len(rest) == 0 || !isNS) {
			return Found{store.Main, first, shortcuts.Vals[first], rest, hides}, nil
		}
		if group, ok := store.Namespace(first); ok {
			if group.Error != nil {
				return Found{}, errors.New(*group.Error)
			}
			gs := group.Shortcuts()
			members := strings.Join(slices.Sorted(slices.Values(gs.Keys)), ", ")
			if members == "" {
				members = "(none)"
			}
			if len(rest) == 0 {
				return Found{}, fmt.Errorf("'%s' is a namespace; run one of its shortcuts: %s", first, members)
			}
			if !has(gs, rest[0]) {
				note := ""
				if hides {
					if yours, ok := home.Namespace(first); ok && has(yours.Shortcuts(), rest[0]) {
						note = fmt.Sprintf(". Your global '%s' has it: shortcut r --global %s %s", first, first, rest[0])
					}
				}
				return Found{}, fmt.Errorf("'%s' not found in namespace '%s'. It has: %s%s", rest[0], first, members, note)
			}
			return Found{group, rest[0], gs.Vals[rest[0]], rest[1:], hides}, nil
		}
	}
	var missing string
	if len(rest) > 0 { // `first` may be meant as a namespace
		missing = fmt.Sprintf("shortcut or namespace '%s' not found. A global namespace is the file %s.",
			first, pyos.Join(home.Main.StoreDir, "imports", first+ImportSuffix))
	} else {
		missing = fmt.Sprintf("shortcut '%s' not found.", first)
	}
	notes := []string{missing}
	if home.Main.Error != nil {
		notes = append(notes, *home.Main.Error)
	}
	root := FindProjectRoot(callerCwd)
	switch {
	case root != "" && skipLocal:
		if hasName(LoadStore(root), first) {
			notes = append(notes, fmt.Sprintf("The project in %s has its own '%s': leave out --global to use it.", root, first))
		}
	case root != "":
		if outer := FindProjectRoot(filepath.Dir(root)); outer != "" && outer != root && hasName(LoadStore(outer), first) {
			notes = append(notes, fmt.Sprintf("The project in %s has a '%s', but only the nearest project, %s, is used here.", outer, first, root))
		}
	default:
		if old := oldProjectFolder(callerCwd); old != "" {
			notes = append(notes, fmt.Sprintf("%s is a folder, which is how shortcut 1.x kept a project. This version reads a %s file, so nothing in that folder is used.", old, ProjectFile))
		}
	}
	return Found{}, errors.New(strings.Join(notes, " "))
}

func has(o *pyjson.Object, k string) bool { _, ok := o.Get(k); return ok }

func hasName(s Store, name string) bool {
	_, ns := s.Namespace(name)
	return has(s.Main.Shortcuts(), name) || ns
}

// ResolveShortcut returns what `shortcut run <words>` runs, and where, and the words left over as
// arguments. An empty callerCwd is the physical cwd.
func ResolveShortcut(words []string, callerCwd string, skipLocal, strict bool) (*ResolvedShortcut, []string, error) {
	if callerCwd == "" {
		callerCwd, _ = pyos.Getwd()
	}
	f, err := FindEntry(words, callerCwd, skipLocal, strict)
	if err != nil {
		return nil, nil, err
	}
	entry, err := LoadEntry(f.Group, f.Name, f.Raw)
	if err != nil {
		return nil, nil, err
	}
	return &ResolvedShortcut{Entry: entry, Group: f.Group, RunCwd: ResolveRunDir(entry.Cwd, f.Group, callerCwd), HidesGlobal: f.Hides}, f.Rest, nil
}

// ResolveName resolves a name for commands that name a shortcut: `name` or `namespace name`, and nothing else.
func ResolveName(words []string, skipLocal bool) (*ResolvedShortcut, error) {
	r, rest, err := ResolveShortcut(words, "", skipLocal, false)
	if err != nil {
		return nil, err
	}
	if len(rest) > 0 {
		return nil, TooManyNames(words)
	}
	return r, nil
}

// TooManyNames is the error of a command that was given more words than a name.
func TooManyNames(words []string) error {
	return fmt.Errorf("too many names: %s", pyos.ShellJoin(words))
}

// ResolvedShortcut is a name looked up: the entry, its group and where it runs.
type ResolvedShortcut struct {
	Entry       *Entry
	Group       Group
	RunCwd      string // computed execution directory
	HidesGlobal bool   // project entry with the same name as a global one
}

func (r ResolvedShortcut) Display() string { return r.Group.Display(r.Entry.Name) }
