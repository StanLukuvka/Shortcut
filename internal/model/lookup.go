package model

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/osutil"
)

// Lookup says which stores a name is looked up in, and how a broken one is treated.
type Lookup struct {
	GlobalOnly bool // --global: skip the project
	Strict     bool // run: refuse every name in a project that can't be used
}

// Found is what FindEntry finds, before anyone judges the entry.
type Found struct {
	Group Group
	Name  string
	Item  Item     // the entry as loaded; Item.Err says whether it can run
	Rest  []string // the words left over, a run's arguments
	Hides bool     // a project's name hides a global one of either kind
}

// FindEntry finds `name` or `namespace name` in the project, then the global store, without judging
// the entry. words must not be empty. lk.Strict (for run) refuses every name inside a project whose file can't be used,
// and a store's load errors for the name it has. Without it (show, rm) a broken file is looked in as far as
// it reads, so you can still see and fix what's there. both lookup failures are just errors here:
// main prints either as "error: <text>".
func FindEntry(c *Context, words []string, lk Lookup) (Found, error) {
	stores := c.Visible(lk)
	first, rest := words[0], words[1:]
	homeIdx := len(stores) - 1
	home := stores[homeIdx]

	for i, store := range stores {
		if lk.Strict && i != homeIdx && store.Main.Error != nil {
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
		if imports, ok := store.Main.Doc.Object("imports"); ok && imports.Has(first) {
			named = true
		}
		if lk.Strict && len(problems) > 0 && named {
			return Found{}, errors.New(strings.Join(problems, "; "))
		}
		// A project's name, shortcut or namespace, hides a global one of either kind
		_, homeNS := home.Namespace(first)
		hides := i != homeIdx && (has(home.Main.Shortcuts(), first) || homeNS)
		shortcuts := store.Main.Shortcuts()
		_, isNS := store.Namespace(first)
		if it, ok := shortcuts.Get(first); ok && (len(rest) == 0 || !isNS) {
			return Found{store.Main, first, it, rest, hides}, nil
		}
		if group, ok := store.Namespace(first); ok {
			if group.Error != nil {
				return Found{}, errors.New(*group.Error)
			}
			gs := group.Shortcuts()
			members := strings.Join(slices.Sorted(slices.Values(gs.Names())), ", ")
			if members == "" {
				members = "(none)"
			}
			if len(rest) == 0 {
				return Found{}, fmt.Errorf("'%s' is a namespace; run one of its shortcuts: %s", first, members)
			}
			it, ok := gs.Get(rest[0])
			if !ok {
				note := ""
				if hides {
					if yours, ok := home.Namespace(first); ok && has(yours.Shortcuts(), rest[0]) {
						note = fmt.Sprintf(". Your global '%s' has it: shortcut r --global %s %s", first, first, rest[0])
					}
				}
				return Found{}, fmt.Errorf("'%s' not found in namespace '%s'. It has: %s%s", rest[0], first, members, note)
			}
			return Found{group, rest[0], it, rest[1:], hides}, nil
		}
	}
	var missing string
	if len(rest) > 0 { // `first` may be meant as a namespace
		missing = fmt.Sprintf("shortcut or namespace '%s' not found. A global namespace is the file %s.",
			first, osutil.Join(home.Main.StoreDir, "imports", first+ImportSuffix))
	} else {
		missing = fmt.Sprintf("shortcut '%s' not found.", first)
	}
	notes := []string{missing}
	if home.Main.Error != nil {
		notes = append(notes, *home.Main.Error)
	}
	root := c.Project
	switch {
	case root != "" && lk.GlobalOnly:
		if hasName(c.Store(root), first) {
			notes = append(notes, fmt.Sprintf("The project in %s has its own '%s': leave out --global to use it.", root, first))
		}
	case root != "":
		if outer := c.FindProjectRoot(filepath.Dir(root)); outer != "" && outer != root && hasName(c.Store(outer), first) {
			notes = append(notes, fmt.Sprintf("The project in %s has a '%s', but only the nearest project, %s, is used here.", outer, first, root))
		}
	}
	return Found{}, errors.New(strings.Join(notes, " "))
}

func has(is Items, name string) bool { _, ok := is.Get(name); return ok }

func hasName(s Store, name string) bool {
	_, ns := s.Namespace(name)
	return has(s.Main.Shortcuts(), name) || ns
}

// ResolveShortcut returns what `shortcut run <words>` runs, and where, and the words left over as
// arguments.
func ResolveShortcut(c *Context, words []string, lk Lookup) (*ResolvedShortcut, []string, error) {
	f, err := FindEntry(c, words, lk)
	if err != nil {
		return nil, nil, err
	}
	if f.Item.Err != nil {
		return nil, nil, f.Item.Err
	}
	entry := f.Item.Entry
	return &ResolvedShortcut{Entry: entry, Group: f.Group, RunCwd: ResolveRunDir(entry.Cwd, f.Group, c.Cwd), HidesGlobal: f.Hides}, f.Rest, nil
}

// ResolveName resolves a name for commands that name a shortcut: `name` or `namespace name`, and nothing else.
func ResolveName(c *Context, words []string, lk Lookup) (*ResolvedShortcut, error) {
	r, rest, err := ResolveShortcut(c, words, Lookup{GlobalOnly: lk.GlobalOnly})
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
	return fmt.Errorf("too many names: %s", osutil.ShellJoin(words))
}

// ResolvedShortcut is a name looked up: the entry, its group and where it runs.
type ResolvedShortcut struct {
	Entry       *Entry
	Group       Group
	RunCwd      string // computed execution directory
	HidesGlobal bool   // project entry with the same name as a global one
}

func (r ResolvedShortcut) Display() string { return r.Group.Display(r.Entry.Name) }
