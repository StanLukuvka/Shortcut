package store

import (
	"maps"
	"path/filepath"
	"slices"

	"github.com/StanLukuvka/Shortcut/internal/platform"
)

// Kind is which of three things a Group is.
type Kind string

const (
	KindGlobal    Kind = "global"    // the global store's shortcuts.json
	KindProject   Kind = "project"   // a project's .shortcuts with .shortcuts.local over it
	KindNamespace Kind = "namespace" // an imported file, in a global or a project store
)

// Source is where a project entry comes from.
type Source string

const (
	SourceShared     Source = "shared"
	SourceLocal      Source = "local"
	SourceOverridden Source = "overridden"
)

// Item is one entry of a file, decoded once at load.
type Item struct {
	Name  string
	Entry *Entry // nil unless the value is an object; fields of the right type are set even when Err is not nil
	Err   error  // why the entry cannot run, nil for a valid entry
	Raw   []byte // the value as written: show --json prints it
}

// Items are the entries of one file, sorted by name.
type Items []Item

func (is Items) Get(name string) (Item, bool) {
	for _, it := range is {
		if it.Name == name {
			return it, true
		}
	}
	return Item{}, false
}

func (is Items) Names() []string {
	names := make([]string, len(is))
	for i, it := range is {
		names[i] = it.Name
	}
	return names
}

// ItemsOf decodes d["shortcuts"] when it is an object; otherwise there are none.
func ItemsOf(d platform.Doc) Items {
	shortcuts, _ := d.Object("shortcuts")
	var items Items
	for _, k := range slices.Sorted(maps.Keys(shortcuts)) {
		e, err := DecodeEntry(k, shortcuts[k])
		items = append(items, Item{Name: k, Entry: e, Err: err, Raw: shortcuts[k]})
	}
	return items
}

// Text is a field of the entry for display: a string as itself, an absent or null field as "", anything else as
// written (5, true, an array), so a wrongly typed field still shows.
func (it Item) Text(field string) string {
	d, ok := platform.ObjectOf(it.Raw)
	if !ok {
		return ""
	}
	if s, ok := d.String(field); ok {
		return s
	}
	if raw := string(d[field]); raw != "null" {
		return raw
	}
	return ""
}

// Group is one set of shortcuts: the global store's shortcuts.json, a project's .shortcuts with
// .shortcuts.local over it, or an imported namespace. StoreDir is the global store or the project's root.
type Group struct {
	Kind      Kind
	InProject bool   // its store is a project's: a KindProject group, or a namespace imported by a project's .shortcuts
	Name      string // namespace name; "" otherwise
	File      string
	StoreDir  string
	Doc       platform.Doc // empty when the file is missing or unreadable
	Shared    Items        // the entries of Doc
	Error     *string      // why the file can't be used
	LocalFile string       // only a project's own group has one
	Local     Items
}

// Shortcuts is what can run: the shared entries, each replaced by the same name in .shortcuts.local, and the local-only ones.
func (g Group) Shortcuts() Items {
	if len(g.Local) == 0 {
		return g.Shared
	}
	out := slices.Clone(g.Shared)
	at := map[string]int{}
	for i, it := range out {
		at[it.Name] = i
	}
	for _, it := range g.Local {
		if i, ok := at[it.Name]; ok {
			out[i] = it
		} else {
			out = append(out, it)
		}
	}
	return out
}

// Source is where a project entry comes from: shared, local or overridden.
func (g Group) Source(name string) Source {
	if _, inLocal := g.Local.Get(name); !inLocal {
		return SourceShared
	}
	if _, inShared := g.Shared.Get(name); inShared {
		return SourceOverridden
	}
	return SourceLocal
}

func (g Group) DefiningFile(name string) string {
	if _, in := g.Local.Get(name); in {
		return g.LocalFile
	}
	return g.File
}

// Folder is where relative 'cwd' values resolve: the folder of the defining file.
func (g Group) Folder() string { return filepath.Dir(platform.Abs(g.File)) }

// ScriptBase: relative paths resolve against a namespace's folder, a project's root, or the global store.
func (g Group) ScriptBase() string {
	if g.Kind == KindNamespace {
		return g.Folder()
	}
	return g.StoreDir
}

// ScriptsDir is a group's own scripts folder, one per namespace: what rm --namespace deletes.
func (g Group) ScriptsDir() string {
	if g.Kind == KindNamespace {
		return platform.Join(g.ScriptBase(), "scripts", g.Name)
	}
	return platform.Join(g.ScriptBase(), "scripts")
}

func (g Group) Display(name string) string {
	if g.Kind == KindNamespace {
		return g.Name + " " + name
	}
	return name
}

func (g Group) Label() string {
	if g.Kind == KindNamespace {
		return g.Name
	}
	return platform.Realpath(g.StoreDir)
}
