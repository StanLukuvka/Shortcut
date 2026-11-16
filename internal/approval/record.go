package approval

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/ordjson"
)

// Record is approvals/<sha256(key)>.json: the snapshot last approved, and Local for .shortcuts.local on its own
// (a change the CLI makes there updates that half alone). Records live in the global store, never next to what they approve.
type Record struct {
	Snapshot map[string]string // shared files: path -> SHA-256 (hex)
	Local    map[string]string // .shortcuts.local half
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

func stringMap(v ordjson.Value) (map[string]string, bool) {
	o, ok := v.(*ordjson.Object)
	if !ok {
		return nil, false
	}
	m := make(map[string]string, len(o.Keys))
	for _, k := range o.Keys {
		s, ok := o.Vals[k].(string)
		if !ok || !isDigest(s) {
			return nil, false
		}
		m[k] = s
	}
	return m, true
}

// Load returns the record, or nil when there is none, it can't be read, or it isn't shaped as one (a
// JSON object with a "snapshot" and a "local" object, each holding only 64-character lowercase hex strings).
func Load(c *model.Context, g model.Group) *Record {
	v, err := model.ReadJSONFile(c.ApprovalPath(g))
	if err != nil {
		return nil
	}
	o, ok := v.(*ordjson.Object)
	if !ok {
		return nil
	}
	snap, ok := stringMap(o.Vals["snapshot"])
	if !ok {
		return nil
	}
	local, ok := stringMap(o.Vals["local"])
	if !ok {
		return nil
	}
	return &Record{Snapshot: snap, Local: local}
}

func sortedObject(m map[string]string) *ordjson.Object {
	o := &ordjson.Object{}
	for _, k := range slices.Sorted(maps.Keys(m)) { // readers sort, so the order is free; sorted is stable
		o.Set(k, m[k])
	}
	return o
}

// Save records what was shown and approved, under the global store's lock.
func Save(c *model.Context, g model.Group, shared, local map[string]string) error {
	if err := checkPaths(shared); err != nil {
		return err
	}
	if err := checkPaths(local); err != nil {
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
		rec := &ordjson.Object{}
		rec.Set("key", g.Key())
		rec.Set("snapshot", sortedObject(shared))
		rec.Set("local", sortedObject(local))
		if err := model.WriteJSONAtomic(path, rec); err != nil {
			return err
		}
		c.Forget(g.Key())
		return nil
	})
}

// LocalIsApproved: whether .shortcuts.local matches what the record holds for it.
func LocalIsApproved(c *model.Context, g model.Group, r *Record) bool {
	var held map[string]string
	if r != nil {
		held = r.Local
	}
	local, err := LocalFiles(g)
	return err == nil && maps.Equal(held, local)
}

// State is the group's approval state, computed once per run (Context.State) from the files on disk: StateOK,
// StateNew (never approved), StateChanged, or StateInvalid (a path or registry text that is not valid UTF-8 can't be
// recorded; Require says which). Anything but StateOK means not approved. Save, and a check under a lock or after a prompt, Forget it first.
func State(c *model.Context, g model.Group) model.ApprovalState {
	return c.State(g.Key(), func() model.ApprovalState { return computeState(c, g) })
}

// Refresh drops the cached state of g and reads it from disk again.
func Refresh(c *model.Context, g model.Group) model.ApprovalState {
	c.Forget(g.Key())
	return State(c, g)
}

func computeState(c *model.Context, g model.Group) model.ApprovalState {
	if !g.NeedsApproval() {
		return model.StateOK
	}
	shared, local, err := snapshot(c, g)
	if err != nil {
		return model.StateInvalid
	}
	r := Load(c, g)
	if r == nil {
		return model.StateNew
	}
	if maps.Equal(r.Snapshot, shared) && maps.Equal(r.Local, local) { // both halves exactly as recorded
		return model.StateOK
	}
	if len(r.Snapshot) > 0 {
		return model.StateChanged
	}
	return model.StateNew // with no snapshot, only CLI writes to .local were recorded
}

// Require returns an error unless the group is approved.
func Require(c *model.Context, g model.Group) error {
	switch State(c, g) {
	case model.StateInvalid:
		return Check(c, g)
	case model.StateNew:
		return fmt.Errorf("%s isn't approved yet; review it with: %s", g.Label(), g.UpdateCmd())
	case model.StateChanged:
		return fmt.Errorf("%s changed since you approved it; run: %s", g.Label(), g.UpdateCmd())
	}
	return nil
}
