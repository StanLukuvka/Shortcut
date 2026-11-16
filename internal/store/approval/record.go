package approval

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/store"
)

// Record is approvals/<sha256(key)>.json: the files last approved, path -> SHA-256 (hex). Records live in the global
// store, never next to what they approve.
type Record struct {
	Files map[string]string
}

func isDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// digests reads an object whose values are all digests.
func digests(d platform.Doc, key string) (map[string]string, bool) {
	o, ok := d.Object(key)
	if !ok {
		return nil, false
	}
	m := make(map[string]string, len(o))
	for k := range o {
		s, ok := o.String(k)
		if !ok || !isDigest(s) {
			return nil, false
		}
		m[k] = s
	}
	return m, true
}

// Load returns the record, or nil when there is none, it can't be read, or it isn't shaped as one (a
// JSON object with a "files" object holding only 64-character lowercase hex strings).
// Keys are matched exactly, not case-insensitively.
func Load(c *store.Context, g store.Group) *Record {
	text, err := platform.ReadJSONFile(c.ApprovalPath(g))
	if err != nil {
		return nil
	}
	d, err := platform.ParseDoc(text)
	if err != nil {
		return nil
	}
	files, ok := digests(d, "files")
	if !ok {
		return nil
	}
	return &Record{Files: files}
}

// mapDoc is m as an object's text.
func mapDoc(m map[string]string) ([]byte, error) {
	d := platform.Doc{}
	for k, v := range m {
		d.SetString(k, v)
	}
	return d.Bytes()
}

// Save records what was shown and approved (what Files returned, which refused paths that JSON cannot hold), under the global store's lock.
func Save(c *store.Context, g store.Group, files map[string]string) error {
	path := c.ApprovalPath(g)
	return c.WithLock(func() error {
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			return err
		}
		rec := platform.Doc{}
		rec.SetString("key", g.Key())
		doc, err := mapDoc(files)
		if err != nil {
			return err
		}
		rec["files"] = doc
		if err := platform.WriteJSONAtomic(path, rec); err != nil {
			return err
		}
		c.Forget(g.Key())
		return nil
	})
}

// State is the group's approval state, computed once per run (Context.State) from the files on disk: StateOK,
// StateNew (never approved), StateChanged, or StateInvalid (a path or registry text that is not valid UTF-8 can't be
// recorded; Require says which). Anything but StateOK means not approved. After a prompt, Forget it first; Save forgets it itself.
func State(c *store.Context, g store.Group) store.ApprovalState {
	return c.State(g.Key(), func() store.ApprovalState { return computeState(c, g) })
}

func computeState(c *store.Context, g store.Group) store.ApprovalState {
	if !g.NeedsApproval() {
		return store.StateOK
	}
	files, err := Files(g)
	if err != nil {
		return store.StateInvalid
	}
	r := Load(c, g)
	if r == nil {
		return store.StateNew
	}
	if maps.Equal(r.Files, files) {
		return store.StateOK
	}
	return store.StateChanged
}

// Require returns an error unless the group is approved.
func Require(c *store.Context, g store.Group) error {
	switch State(c, g) {
	case store.StateInvalid:
		return Check(c, g)
	case store.StateNew:
		return fmt.Errorf("%s isn't approved yet; review it with: %s", g.Label(), g.UpdateCmd())
	case store.StateChanged:
		return fmt.Errorf("%s changed since you approved it; run: %s", g.Label(), g.UpdateCmd())
	}
	return nil
}
