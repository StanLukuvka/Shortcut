package approval

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"unicode/utf8"

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
func digests(d *platform.Doc, key string) (map[string]string, bool) {
	o, ok := d.Object(key)
	if !ok {
		return nil, false
	}
	m := make(map[string]string, len(o.Keys))
	for _, k := range o.Keys {
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

// mapDoc is m as an object's text, its keys sorted: readers sort, so the order is free, and sorted is stable.
func mapDoc(m map[string]string) ([]byte, error) {
	d := &platform.Doc{}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if err := d.SetString(k, m[k]); err != nil {
			return nil, err
		}
	}
	return d.Bytes()
}

// Save records what was shown and approved, under the global store's lock.
func Save(c *store.Context, g store.Group, files map[string]string) error {
	if err := checkPaths(files); err != nil {
		return err
	}
	if err := CheckKey(c, g); err != nil {
		return err
	}
	if !utf8.ValidString(g.Key()) { // a project in a folder whose name is not valid UTF-8: the record could not hold its key
		return fmt.Errorf("cannot approve: %q is not valid UTF-8; rename it", g.Key())
	}
	path := c.ApprovalPath(g)
	return c.WithRegistryLock(c.Global, func() error {
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			return err
		}
		rec := &platform.Doc{}
		if err := rec.SetString("key", g.Key()); err != nil {
			return err
		}
		doc, err := mapDoc(files)
		if err != nil {
			return err
		}
		rec.Set("files", doc)
		if err := platform.WriteJSONAtomic(path, rec); err != nil {
			return err
		}
		c.Forget(g.Key())
		return nil
	})
}

// State is the group's approval state, computed once per run (Context.State) from the files on disk: StateOK,
// StateNew (never approved), StateChanged, or StateInvalid (a path or registry text that is not valid UTF-8 can't be
// recorded; Require says which). Anything but StateOK means not approved. Save, and a check under a lock or after a prompt, Forget it first.
func State(c *store.Context, g store.Group) store.ApprovalState {
	return c.State(g.Key(), func() store.ApprovalState { return computeState(c, g) })
}

func computeState(c *store.Context, g store.Group) store.ApprovalState {
	if !g.NeedsApproval() {
		return store.StateOK
	}
	files, err := snapshot(c, g)
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
