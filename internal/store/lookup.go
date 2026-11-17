package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/platform"
)

// Scope is the one store a name is looked up in. There is no fallback from one to the other.
type Scope uint8

const (
	ScopeGlobal  Scope = iota // your own shortcuts and global namespaces
	ScopeProject              // the nearest project: its .shortcuts with .shortcuts.local over it, and what it imports
)

// Lookup says which store a name is looked up in, and how a broken one is treated.
type Lookup struct {
	Scope  Scope
	Strict bool   // run: refuse every name in a project that can't be used
	Cmd    string // the command a hint at the other scope names: "r", "show" or "rm"; "" gives no hint
}

// SplitScope reads the dot: a first word "." selects the project, and is not part of the name.
func SplitScope(words []string) (Scope, []string) {
	if len(words) > 0 && words[0] == "." {
		return ScopeProject, words[1:]
	}
	return ScopeGlobal, words
}

// Found is what FindEntry finds, before anyone judges the entry.
type Found struct {
	Group Group
	Name  string
	Item  Item     // the entry as loaded; Item.Err says whether it can run
	Rest  []string // the words left over, a run's arguments
}

// otherHas reports whether st would resolve words: a shortcut, or a namespace and its shortcut.
func otherHas(st Store, words []string) bool {
	first, rest := words[0], words[1:]
	group, isNS := st.Namespace(first)
	if has(st.Main.Shortcuts(), first) && (len(rest) == 0 || !isNS) {
		return true
	}
	return isNS && (len(rest) == 0 || has(group.Shortcuts(), rest[0]))
}

// otherHint is the sentence that says the other scope has words and how to reach it, "" when it doesn't or when
// the command gives no hint.
func otherHint(c *Context, lk Lookup, words []string) string {
	if lk.Cmd == "" {
		return ""
	}
	joined := platform.ShellJoin(words)
	if lk.Cmd == "rm" { // rm changes only the global store and .shortcuts.local, so it names no command for the project
		if c.Project == "" {
			return ""
		}
		if _, ok := c.Store(c.Project).Main.Local.Get(words[0]); ok && len(words) == 1 {
			return fmt.Sprintf("Your %s in %s has it: shortcut rm --local %s, from %s", LocalFile, c.Project, joined, c.Project)
		}
		if otherHas(c.Store(c.Project), words) {
			return fmt.Sprintf("The project in %s has it; rm changes only your global store and your %s", c.Project, LocalFile)
		}
		return ""
	}
	if lk.Scope == ScopeProject {
		if otherHas(c.Store(c.Global), words) {
			return fmt.Sprintf("Your global store has it: shortcut %s %s", lk.Cmd, joined)
		}
	} else if c.Project != "" && otherHas(c.Store(c.Project), words) {
		return fmt.Sprintf("The project in %s has it: shortcut %s . %s", c.Project, lk.Cmd, joined)
	}
	return ""
}

// FindEntry finds `name` or `namespace name` in the one store lk.Scope names, without judging the entry. words must
// not be empty. lk.Strict (for run) refuses every name inside a project whose file can't be used, and a store's load
// errors for the name it has. Without it (show, rm) a broken file is looked in as far as it reads, so you can still
// see and fix what's there. both lookup failures are just errors here: the caller prints either as "error: <text>".
func FindEntry(c *Context, words []string, lk Lookup) (Found, error) {
	first, rest := words[0], words[1:]
	var store Store
	if lk.Scope == ScopeProject {
		if c.ProjectErr != nil {
			return Found{}, c.ProjectErr
		}
		if c.Project == "" {
			return Found{}, errors.New("no project here: no " + ProjectFile + " or " + LocalFile + " in this folder or its parents")
		}
		store = c.Store(c.Project)
		if lk.Strict && store.Main.Error != nil {
			return Found{}, fmt.Errorf("%s. Nothing runs inside this project until it's fixed.", *store.Main.Error)
		}
	} else {
		store = c.Store(c.Global)
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
	shortcuts := store.Main.Shortcuts()
	_, isNS := store.Namespace(first)
	if it, ok := shortcuts.Get(first); ok && (len(rest) == 0 || !isNS) {
		return Found{store.Main, first, it, rest}, nil
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
			if hint := otherHint(c, lk, words); hint != "" {
				note = ". " + hint
			}
			return Found{}, fmt.Errorf("'%s' not found in namespace '%s'. It has: %s%s", rest[0], first, members, note)
		}
		return Found{group, rest[0], it, rest[1:]}, nil
	}
	var missing string
	switch {
	case len(rest) > 0 && lk.Scope == ScopeGlobal: // `first` may be meant as a namespace
		missing = fmt.Sprintf("shortcut or namespace '%s' not found. A global namespace is the file %s.",
			first, platform.Join(store.Main.StoreDir, "imports", first+ImportSuffix))
	case len(rest) > 0:
		missing = fmt.Sprintf("shortcut or namespace '%s' not found.", first)
	default:
		missing = fmt.Sprintf("shortcut '%s' not found.", first)
	}
	notes := []string{missing}
	if store.Main.Error != nil {
		notes = append(notes, *store.Main.Error)
	}
	if hint := otherHint(c, lk, words); hint != "" {
		notes = append(notes, hint)
	}
	if root := c.Project; root != "" && lk.Scope == ScopeProject {
		outer, err := c.FindProjectRoot(filepath.Dir(root))
		if err != nil {
			notes = append(notes, err.Error())
		} else if outer != "" && outer != root && hasName(c.Store(outer), first) {
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
	runCwd, err := ResolveRunDir(entry.Cwd, f.Group, c.Cwd)
	if err != nil {
		return nil, nil, err
	}
	return &ResolvedShortcut{Entry: entry, Group: f.Group, RunCwd: runCwd}, f.Rest, nil
}

// ResolveName resolves a name for commands that name a shortcut: `name` or `namespace name`, and nothing else.
func ResolveName(c *Context, words []string, lk Lookup) (*ResolvedShortcut, error) {
	r, rest, err := ResolveShortcut(c, words, Lookup{Scope: lk.Scope, Cmd: lk.Cmd})
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
	return fmt.Errorf("too many names: %s", platform.ShellJoin(words))
}

// ResolvedShortcut is a name looked up: the entry, its group and where it runs.
type ResolvedShortcut struct {
	Entry  *Entry
	Group  Group
	RunCwd string // computed execution directory
}

func (r ResolvedShortcut) Display() string { return r.Group.Display(r.Entry.Name) }
