// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (G1-5). Don't hand-edit without checking the plan.

package approval

import (
	"fmt"
	"os"

	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
)

// Record is approvals/<sha256(key)>.json, written by Python today and still readable and writable: the hash and
// snapshot last approved, and Local for .shortcuts.local on its own (a change the CLI makes there updates that half
// alone). Records live in the global store, never next to what they approve.
type Record struct {
	Key      string
	Hash     string            // FilesHash of the shared files; "" if the file's hash isn't a string
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

// Load is load_approval: the record, or nil when there is none, it can't be read, or it isn't shaped as one (a
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
	r.Hash, _ = o.Vals["hash"].(string)
	if l, present := o.Get("local"); present {
		if r.Local, ok = stringMap(l); !ok {
			return nil
		}
	}
	return r
}

func sortedObject(m map[string]string) *pyjson.Object {
	o := &pyjson.Object{}
	for _, k := range sortedKeys(m) { // readers sort, so the order is free; sorted is stable
		o.Set(k, m[k])
	}
	return o
}

// Save is save_approval: it records what was shown and approved, under the global store's lock.
func Save(g model.Group, shared, local map[string]string) error {
	path := model.ApprovalPath(g)
	return model.WithRegistryLock(model.GlobalStoreDir(), func() error {
		if err := os.MkdirAll(pyos.Dirname(path), 0o777); err != nil {
			return err
		}
		rec := &pyjson.Object{}
		rec.Set("key", g.Key())
		rec.Set("hash", FilesHash(shared))
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
	return FilesHash(held) == FilesHash(LocalFiles(g))
}

// State is approval_state: "ok", "new" (never approved) or "changed".
func State(g model.Group) string {
	if !g.NeedsApproval() {
		return "ok"
	}
	r := Load(g)
	if r == nil {
		return "new"
	}
	if r.Hash == FilesHash(SharedFiles(g)) && LocalIsApproved(g, r) {
		return "ok"
	}
	if len(r.Snapshot) > 0 {
		return "changed"
	}
	return "new" // with no snapshot, only CLI writes to .local were recorded
}

// Require is require_approved.
func Require(g model.Group) error {
	switch State(g) {
	case "new":
		return fmt.Errorf("%s isn't approved yet; review it with: %s", g.Label(), g.UpdateCmd())
	case "changed":
		return fmt.Errorf("%s changed since you approved it; run: %s", g.Label(), g.UpdateCmd())
	}
	return nil
}
