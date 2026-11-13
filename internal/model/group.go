package model

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
)

// Group is one set of shortcuts: the global store's shortcuts.json (Name ""), a project's .shortcuts with
// .shortcuts.local over it, or an imported namespace. StoreDir is the global store or the project's root.
type Group struct {
	Name      string // "" = main group
	File      string
	StoreDir  string
	Data      *pyjson.Object // empty object when the file is missing or unreadable; nil is read as empty too
	Error     *string        // why the file can't be used
	LocalFile string         // only a project's own group has one
	LocalData *pyjson.Object
}

// shortcutsIn is data["shortcuts"] when it is an object, else an empty one.
func shortcutsIn(data *pyjson.Object) *pyjson.Object {
	if data != nil {
		if o, ok := data.Vals["shortcuts"].(*pyjson.Object); ok {
			return o
		}
	}
	return &pyjson.Object{}
}

func (g Group) SharedShortcuts() *pyjson.Object { return shortcutsIn(g.Data) }

// LocalShortcuts is raw, so a null that hides a shared entry is still there.
func (g Group) LocalShortcuts() *pyjson.Object { return shortcutsIn(g.LocalData) }

// Shortcuts is what can run: the shared entries, each replaced or hidden by .shortcuts.local.
func (g Group) Shortcuts() *pyjson.Object {
	shared, local := g.SharedShortcuts(), g.LocalShortcuts()
	out := &pyjson.Object{}
	for _, k := range shared.Keys {
		out.Set(k, shared.Vals[k])
	}
	for _, k := range local.Keys {
		if local.Vals[k] == nil {
			out.Delete(k)
		} else {
			out.Set(k, local.Vals[k])
		}
	}
	return out
}

// Source is where a project entry comes from: shared, local, overridden or hidden.
func (g Group) Source(name string) string {
	local := g.LocalShortcuts()
	v, inLocal := local.Get(name)
	if !inLocal {
		return "shared"
	}
	if v == nil {
		return "hidden"
	}
	if _, inShared := g.SharedShortcuts().Get(name); inShared {
		return "overridden"
	}
	return "local"
}

func (g Group) DefiningFile(name string) string {
	if _, in := g.LocalShortcuts().Get(name); in {
		return g.LocalFile
	}
	return g.File
}

func (g Group) IsProject() bool { return !IsGlobalStore(g.StoreDir) }

// Folder is where relative 'cwd' values resolve: the folder of the defining file.
func (g Group) Folder() string { return pyos.Dirname(pyos.Abs(g.File)) }

// ScriptBase: relative paths resolve against a namespace's folder, a project's root, or the global store.
func (g Group) ScriptBase() string {
	if g.Name != "" {
		return g.Folder()
	}
	return g.StoreDir
}

// ScriptsDir is where shortcut keeps its copies of a group's scripts, one folder per namespace.
func (g Group) ScriptsDir() string {
	if g.Name != "" {
		return pyos.Join(g.ScriptBase(), "scripts", g.Name)
	}
	return pyos.Join(g.ScriptBase(), "scripts")
}

func (g Group) Display(name string) string {
	if g.Name != "" {
		return g.Name + " " + name
	}
	return name
}

func (g Group) NeedsApproval() bool { return g.Name != "" || !IsGlobalStore(g.StoreDir) }

// Key is what the approval is recorded against: the project's path, or for a namespace the store it's in, its name
// there and its file. Pointing a name at another file, even one approved elsewhere, is new.
func (g Group) Key() string {
	store := pyos.Realpath(g.StoreDir)
	if g.Name == "" {
		return store
	}
	return pyjson.DumpsCompact([]pyjson.Value{store, g.Name, pyos.Realpath(g.File)})
}

func (g Group) Label() string {
	if g.Name != "" {
		return g.Name
	}
	return g.Key()
}

func (g Group) UpdateCmd() string {
	if g.Name != "" {
		return "shortcut update " + g.Name
	}
	return "shortcut update"
}

// RecordName is the approval record's file name: sha256(key) hex + ".json". Python hashes key.encode(), which
// raises for a project path that isn't valid UTF-8, so no such record exists there; Go hashes the key's raw bytes
// (what os.fsencode would give).
func RecordName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:]) + ".json"
}

// ApprovalPath is <global store>/approvals/<RecordName(key)>.
func ApprovalPath(g Group) string {
	return pyos.Join(GlobalStoreDir(), approvalsDir, RecordName(g.Key()))
}
