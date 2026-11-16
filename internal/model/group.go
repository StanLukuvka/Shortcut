package model

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/ordjson"
	"github.com/StanLukuvka/Shortcut/internal/osutil"
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

// Group is one set of shortcuts: the global store's shortcuts.json, a project's .shortcuts with
// .shortcuts.local over it, or an imported namespace. StoreDir is the global store or the project's root.
type Group struct {
	Kind      Kind
	InProject bool   // its store is a project's: a KindProject group, or a namespace imported by a project's .shortcuts
	Name      string // namespace name; "" otherwise
	File      string
	StoreDir  string
	Data      *ordjson.Object // empty object when the file is missing or unreadable
	Error     *string         // why the file can't be used
	LocalFile string          // only a project's own group has one
	LocalData *ordjson.Object
}

// shortcutsIn is data["shortcuts"] when it is an object, else an empty one.
func shortcutsIn(data *ordjson.Object) *ordjson.Object {
	if o, ok := data.Vals["shortcuts"].(*ordjson.Object); ok {
		return o
	}
	return &ordjson.Object{}
}

func (g Group) SharedShortcuts() *ordjson.Object { return shortcutsIn(g.Data) }

// A namespace or the global store has no LocalData, so nil is empty here.
func (g Group) LocalShortcuts() *ordjson.Object {
	if g.LocalData == nil {
		return &ordjson.Object{}
	}
	return shortcutsIn(g.LocalData)
}

// Shortcuts is what can run: the shared entries, each replaced by the same name in .shortcuts.local, and the local-only ones.
func (g Group) Shortcuts() *ordjson.Object {
	shared, local := g.SharedShortcuts(), g.LocalShortcuts()
	out := &ordjson.Object{}
	for _, k := range shared.Keys {
		out.Set(k, shared.Vals[k])
	}
	for _, k := range local.Keys {
		out.Set(k, local.Vals[k])
	}
	return out
}

// Source is where a project entry comes from: shared, local or overridden.
func (g Group) Source(name string) Source {
	local := g.LocalShortcuts()
	if _, inLocal := local.Get(name); !inLocal {
		return SourceShared
	}
	if _, inShared := g.SharedShortcuts().Get(name); inShared {
		return SourceOverridden
	}
	return SourceLocal
}

func (g Group) DefiningFile(name string) string {
	if _, in := g.LocalShortcuts().Get(name); in {
		return g.LocalFile
	}
	return g.File
}

// Folder is where relative 'cwd' values resolve: the folder of the defining file.
func (g Group) Folder() string { return filepath.Dir(osutil.Abs(g.File)) }

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
		return osutil.Join(g.ScriptBase(), "scripts", g.Name)
	}
	return osutil.Join(g.ScriptBase(), "scripts")
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
	store := osutil.Realpath(g.StoreDir)
	if g.Kind != KindNamespace {
		return store
	}
	return keyText(store, g.Name, osutil.Realpath(g.File))
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
