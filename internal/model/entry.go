// Package model holds shortcut's data: entries, groups, stores, and the files they are read from and written to.
// It never prints and never imports cli.
package model

import (
	"fmt"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/ordjson"
	"github.com/StanLukuvka/Shortcut/internal/osutil"
)

const (
	RegistryVersion = 1
	Caller          = "caller"    // 'cwd' value, and the default: run in the user's current directory
	ImportSuffix    = ".shortcut" // an imported namespace, in the same JSON format as shortcuts.json
	bareImport      = "a namespace file needs a name, e.g. docker.shortcut"
	ProjectFile     = ".shortcuts"       // shared and committed; shortcut never writes it
	LocalFile       = ".shortcuts.local" // personal; overlays ProjectFile entry by entry
	approvalsDir    = "approvals"
)

// Entry is one shortcut's definition.
type Entry struct {
	Name        string
	Description string  // "" allowed; always written
	Inline      *string // exactly one of Inline and Script is non-nil
	Script      *string
	Cwd         *string
	Confirm     bool
}

var knownFields = map[string]bool{
	"description": true, "inline": true, "script": true, "cwd": true, "confirm": true,
}

var textFields = []string{"inline", "script", "cwd"}

// EntryFromValue builds an Entry from a decoded value, with fixed messages and check order.
func EntryFromValue(name string, v ordjson.Value) (*Entry, error) {
	bad := func(problem string) error { return fmt.Errorf("'%s' is not a valid entry: %s", name, problem) }
	data, ok := v.(*ordjson.Object)
	if !ok {
		return nil, bad("expected a dictionary")
	}
	// An ignored field could be a safety setting from a newer version, so never ignore one
	var unknown []string
	for _, k := range data.Keys {
		if !knownFields[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return nil, bad("unknown field: " + strings.Join(slices.Sorted(slices.Values(unknown)), ", "))
	}
	e := &Entry{Name: name}
	if d, present := data.Get("description"); present {
		s, isStr := d.(string)
		if !isStr {
			return nil, bad("'description' must be a string")
		}
		e.Description = s
	}
	texts := map[string]*string{}
	for _, f := range textFields {
		val, present := data.Get(f)
		if !present {
			continue
		}
		s, isStr := val.(string)
		if !isStr {
			return nil, bad(fmt.Sprintf("'%s' must be a string", f))
		}
		texts[f] = &s
	}
	confirm := false
	if val, present := data.Get("confirm"); present {
		b, isBool := val.(bool)
		if !isBool {
			return nil, bad("'confirm' must be a boolean")
		}
		confirm = b
	}
	if (texts["inline"] == nil) == (texts["script"] == nil) {
		return nil, bad("it needs exactly one of 'inline' and 'script'")
	}
	e.Inline, e.Script, e.Cwd = texts["inline"], texts["script"], texts["cwd"]
	e.Confirm = confirm
	return e, nil
}

// ToObject returns the entry as an object: description, then the text fields that are set, confirm.
func (e *Entry) ToObject() *ordjson.Object {
	o := &ordjson.Object{}
	o.Set("description", e.Description)
	for i, p := range []*string{e.Inline, e.Script, e.Cwd} {
		if p != nil {
			o.Set(textFields[i], *p)
		}
	}
	if e.Confirm {
		o.Set("confirm", true)
	}
	return o
}

// IsRelativeCwd: a cwd that means a folder next to the file defining it, so it's only right in that checkout.
func IsRelativeCwd(cwd *string) bool {
	return cwd != nil && *cwd != "" && *cwd != Caller && !strings.HasPrefix(osutil.ExpandUser(*cwd), "/")
}
