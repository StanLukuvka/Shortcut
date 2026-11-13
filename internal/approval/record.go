package approval

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
)

// Record is approvals/<sha256(key)>.json, written by Python today and still readable and writable: the snapshot
// last approved, and Local for .shortcuts.local on its own (a change the CLI makes there updates that half alone).
// Records live in the global store, never next to what they approve. Older records also hold a "hash" field; it is
// ignored, since the snapshot is compared exactly.
type Record struct {
	Key      string
	Snapshot map[string]string // shared files: path -> reviewable text
	Local    map[string]string // .shortcuts.local half; absent in old records = empty
}

func stringMap(v pyjson.Value) (map[string]string, bool) {
	o, ok := v.(*pyjson.Object)
	if !ok {
		return nil, false
	}
	m := make(map[string]string, len(o.Keys))
	for _, k := range o.Keys {
		s, ok := o.Vals[k].(string)
		if !ok {
			return nil, false
		}
		m[k] = s
	}
	return m, true
}

// Load returns the record, or nil when there is none, it can't be read, or it isn't shaped as one (a
// JSON object with a "snapshot" object).
// lazy: Python would crash later on a record whose snapshot or local holds something but strings; here that is "no record".
func Load(g model.Group) *Record {
	v, err := model.ReadJSONFile(model.ApprovalPath(g))
	if err != nil {
		return nil
	}
	o, ok := v.(*pyjson.Object)
	if !ok {
		return nil
	}
	snap, ok := stringMap(o.Vals["snapshot"])
	if !ok {
		return nil
	}
	r := &Record{Snapshot: snap, Local: map[string]string{}}
	r.Key, _ = o.Vals["key"].(string)
	if l, present := o.Get("local"); present {
		if r.Local, ok = stringMap(l); !ok {
			return nil
		}
	}
	return r
}

func sortedObject(m map[string]string) *pyjson.Object {
	o := &pyjson.Object{}
	for _, k := range slices.Sorted(maps.Keys(m)) { // readers sort, so the order is free; sorted is stable
		o.Set(k, m[k])
	}
	return o
}

// Save records what was shown and approved, under the global store's lock.
func Save(g model.Group, shared, local map[string]string) error {
	if err := checkPaths(shared); err != nil {
		return err
	}
	if err := checkPaths(local); err != nil {
		return err
	}
	if err := CheckKey(g); err != nil {
		return err
	}
	if !utf8.ValidString(g.Key()) { // a project in a folder whose name is not valid UTF-8: the record could not hold its key
		return fmt.Errorf("cannot approve: %q is not valid UTF-8; rename it", g.Key())
	}
	path := model.ApprovalPath(g)
	return model.WithRegistryLock(model.GlobalStoreDir(), func() error {
		if err := os.MkdirAll(pyos.Dirname(path), 0o777); err != nil {
			return err
		}
		rec := &pyjson.Object{}
		rec.Set("key", g.Key())
		rec.Set("snapshot", sortedObject(shared))
		rec.Set("local", sortedObject(local))
		return model.WriteJSONAtomic(path, rec)
	})
}

// LocalIsApproved: whether .shortcuts.local matches what the record holds for it.
func LocalIsApproved(g model.Group, r *Record) bool {
	var held map[string]string
	if r != nil {
		held = r.Local
	}
	local, err := LocalFiles(g)
	return err == nil && maps.Equal(held, local)
}

// State returns "ok", "new" (never approved), "changed", or "invalid" (text that is not valid UTF-8 cannot be
// approved; Require says which). Anything but "ok" means not approved.
func State(g model.Group) string {
	if !g.NeedsApproval() {
		return "ok"
	}
	shared, local, err := snapshot(g)
	if err != nil {
		return "invalid"
	}
	r := Load(g)
	if r == nil {
		return "new"
	}
	if maps.Equal(r.Snapshot, shared) && maps.Equal(r.Local, local) { // both halves exactly as recorded
		return "ok"
	}
	if len(r.Snapshot) > 0 {
		return "changed"
	}
	return "new" // with no snapshot, only CLI writes to .local were recorded
}

// Require returns an error unless the group is approved.
func Require(g model.Group) error {
	switch State(g) {
	case "invalid":
		return Check(g)
	case "new":
		return fmt.Errorf("%s isn't approved yet; review it with: %s", g.Label(), g.UpdateCmd())
	case "changed":
		return fmt.Errorf("%s changed since you approved it; run: %s", g.Label(), g.UpdateCmd())
	}
	return nil
}
