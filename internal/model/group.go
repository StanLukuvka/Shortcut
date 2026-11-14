package model

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/ordjson"
	"github.com/StanLukuvka/Shortcut/internal/osutil"
)

// Group is one set of shortcuts: the global store's shortcuts.json (Name ""), a project's .shortcuts with
// .shortcuts.local over it, or an imported namespace. StoreDir is the global store or the project's root.
type Group struct {
	Name      string // "" = main group
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

// LocalShortcuts is raw, so a null that hides a shared entry is still there.
// A namespace or the global store has no LocalData, so nil is empty here.
func (g Group) LocalShortcuts() *ordjson.Object {
	if g.LocalData == nil {
		return &ordjson.Object{}
	}
	return shortcutsIn(g.LocalData)
}

// Shortcuts is what can run: the shared entries, each replaced or hidden by .shortcuts.local.
func (g Group) Shortcuts() *ordjson.Object {
	shared, local := g.SharedShortcuts(), g.LocalShortcuts()
	out := &ordjson.Object{}
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
func (g Group) Folder() string { return filepath.Dir(osutil.Abs(g.File)) }

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
		return osutil.Join(g.ScriptBase(), "scripts", g.Name)
	}
	return osutil.Join(g.ScriptBase(), "scripts")
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
	store := osutil.Realpath(g.StoreDir)
	if g.Name == "" {
		return store
	}
	return keyJSON(store, g.Name, osutil.Realpath(g.File))
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
	return osutil.Join(GlobalStoreDir(), approvalsDir, RecordName(g.Key()))
}

const hexDigits = "0123456789abcdef"

// keyJSON is json.dumps(parts) as Python writes it: ", " between the parts, ASCII only. It names the approval record of
// a namespace, so it must not change, and it must tell two different paths apart whatever bytes they hold; the JSON
// encoder, which refuses invalid UTF-8, is not used for it.
func keyJSON(parts ...string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, p := range parts {
		if i > 0 {
			b.WriteString(", ")
		}
		writeKeyString(&b, p)
	}
	b.WriteByte(']')
	return b.String()
}

// writeKeyString writes s as a JSON string literal the way Python does. s is read as Python's str: UTF-8, a WTF-8
// surrogate triple as that surrogate, any other invalid byte 0xNN as U+DC00+NN. Every code point outside space..~
// except the escapes below becomes \uXXXX (lower hex, astral as a surrogate pair).
func writeKeyString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, n := keyRune(s[i:])
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			writeU(b, r)
		case r >= 0x20 && r <= 0x7e:
			b.WriteByte(byte(r))
		case r >= 0x10000:
			r -= 0x10000
			writeU(b, 0xD800+(r>>10))
			writeU(b, 0xDC00+(r&0x3FF))
		default:
			writeU(b, r)
		}
		i += n
	}
	b.WriteByte('"')
}

func writeU(b *strings.Builder, r rune) {
	b.WriteString(`\u`)
	for shift := 12; shift >= 0; shift -= 4 {
		b.WriteByte(hexDigits[(r>>uint(shift))&0xF])
	}
}

// keyRune reads one code point of a key: UTF-8, or an invalid byte 0xNN as U+DC00+NN (the escape form of old stores). A WTF-8
// triple for a lone surrogate other than U+DC80..U+DCFF (ED A0 80 .. ED B1 BF and ED B4 80 .. ED BF BF) is read as
// that surrogate; ED B2 xx and ED B3 xx are just invalid bytes. A lone invalid byte is one escape; the triple is one
// escape for three bytes. Do not rewrite it per byte: that would change the Keys of records already on disk.
// lazy: a raw path holding bytes ED A0..B1 xx or ED B4..BF xx is read as a surrogate where Python would escape three
// bytes; such a path is not valid UTF-8 and never a real file name in practice.
func keyRune(s string) (rune, int) {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && n == 1 {
		if len(s) >= 3 && s[0] == 0xED && s[1] >= 0xA0 && s[1] <= 0xBF && s[1] != 0xB2 && s[1] != 0xB3 && s[2]&0xC0 == 0x80 {
			return 0xD000 | rune(s[1]&0x3F)<<6 | rune(s[2]&0x3F), 3
		}
		return 0xDC00 + rune(s[0]), 1
	}
	return r, n
}
