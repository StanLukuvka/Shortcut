package store

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/platform"
)

// Kind is which of three things a Group is.
type Kind string

const (
	KindGlobal    Kind = "global"    // the global store's shortcuts.json
	KindProject   Kind = "project"   // a project's .shortcuts with .shortcuts.local over it
	KindNamespace Kind = "namespace" // an imported file, in a global or a project store
)

// ApprovalState is where a group stands against its recorded approval.
type ApprovalState string

const (
	StateOK      ApprovalState = "ok"
	StateNew     ApprovalState = "new"
	StateChanged ApprovalState = "changed"
	StateInvalid ApprovalState = "invalid" // a path or registry text that is not valid UTF-8 cannot be recorded; script contents are hashed as bytes
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
	Raw   []byte // the value as written, compact: show --json prints it
}

// Items are the entries of one file, in file order.
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
func ItemsOf(d *platform.Doc) Items {
	shortcuts, _ := d.Object("shortcuts")
	var items Items
	for _, k := range shortcuts.Keys {
		e, err := DecodeEntry(k, shortcuts.Raw[k])
		items = append(items, Item{Name: k, Entry: e, Err: err, Raw: shortcuts.Raw[k]})
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
	if raw := string(d.Raw[field]); raw != "null" {
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
	Doc       *platform.Doc // empty when the file is missing or unreadable
	Shared    Items         // the entries of Doc
	Error     *string       // why the file can't be used
	LocalFile string        // only a project's own group has one
	LocalDoc  *platform.Doc
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

// ScriptsDir is a group's own scripts folder, one per namespace: what rm --namespace deletes and approval covers.
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

func (g Group) NeedsApproval() bool { return g.Kind != KindGlobal }

// Key is what the approval is recorded against: the project's path, or for a namespace the store it's in, its name
// there and its file. Pointing a name at another file, even one approved elsewhere, is new.
func (g Group) Key() string {
	store := platform.Realpath(g.StoreDir)
	if g.Kind != KindNamespace {
		return store
	}
	return keyText(store, g.Name, platform.Realpath(g.File))
}

func (g Group) Label() string {
	if g.Kind == KindNamespace {
		return g.Name
	}
	return g.Key()
}

func (g Group) UpdateCmd() string {
	if g.Kind == KindNamespace {
		return "shortcut update " + g.Name
	}
	return "shortcut update"
}

// RecordName is the approval record's file name: sha256(key) hex + ".json".
func RecordName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:]) + ".json"
}

// keyText is the text a namespace's approval record is named after: the parts, each strconv.QuoteToASCII-quoted,
// in brackets. The quoting is injective for any bytes (an invalid byte becomes \xNN, a backslash is escaped) and
// ASCII only, so two different paths never share a key and a Go upgrade cannot change it.
func keyText(parts ...string) string {
	q := make([]string, len(parts))
	for i, p := range parts {
		q[i] = strconv.QuoteToASCII(p)
	}
	return "[" + strings.Join(q, ", ") + "]"
}
