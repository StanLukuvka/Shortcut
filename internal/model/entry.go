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

// Field is one entry field.
type Field struct {
	Name string
	Type string // "string" or "boolean", the words docs/shortcut.schema.json uses
	Show bool   // `show --field` reads it by its name
}

// Fields are the entry's fields, in the order they are checked and written. Validation, ToObject and
// `show --field` read them, and a test checks docs/shortcut.schema.json against them.
func Fields() []Field {
	return []Field{
		{"description", "string", true}, {"inline", "string", true}, {"script", "string", true},
		{"cwd", "string", false}, {"confirm", "boolean", false},
	}
}

// EntryFromValue builds an Entry from a decoded value, with fixed messages and check order.
func EntryFromValue(name string, v ordjson.Value) (*Entry, error) {
	bad := func(problem string) error { return fmt.Errorf("'%s' is not a valid entry: %s", name, problem) }
	data, ok := v.(*ordjson.Object)
	if !ok {
		return nil, bad("expected a dictionary")
	}
	// An ignored field could be a safety setting from a newer version, so never ignore one
	fields := Fields()
	var unknown []string
	for _, k := range data.Keys {
		if !slices.ContainsFunc(fields, func(f Field) bool { return f.Name == k }) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return nil, bad("unknown field: " + strings.Join(slices.Sorted(slices.Values(unknown)), ", "))
	}
	vals := map[string]any{}
	for _, f := range fields {
		val, present := data.Get(f.Name)
		if !present {
			continue
		}
		var typed bool
		switch f.Type {
		case "string":
			vals[f.Name], typed = val.(string)
		case "boolean":
			vals[f.Name], typed = val.(bool)
		}
		if !typed {
			return nil, bad("'" + f.Name + "' must be a " + f.Type)
		}
	}
	if (vals["inline"] == nil) == (vals["script"] == nil) {
		return nil, bad("it needs exactly one of 'inline' and 'script'")
	}
	text := func(k string) *string {
		if s, ok := vals[k].(string); ok {
			return &s
		}
		return nil
	}
	description, _ := vals["description"].(string)
	confirm, _ := vals["confirm"].(bool)
	return &Entry{Name: name, Description: description, Inline: text("inline"), Script: text("script"),
		Cwd: text("cwd"), Confirm: confirm}, nil
}

// ToObject returns the entry as an object, its fields in table order: description always, the others when set.
func (e *Entry) ToObject() *ordjson.Object {
	text := map[string]*string{"inline": e.Inline, "script": e.Script, "cwd": e.Cwd}
	o := &ordjson.Object{}
	for _, f := range Fields() {
		switch {
		case f.Name == "description":
			o.Set(f.Name, e.Description)
		case f.Name == "confirm":
			if e.Confirm {
				o.Set(f.Name, true)
			}
		case text[f.Name] != nil:
			o.Set(f.Name, *text[f.Name])
		}
	}
	return o
}

// IsRelativeCwd: a cwd that means a folder next to the file defining it, so it's only right in that checkout.
func IsRelativeCwd(cwd *string) bool {
	return cwd != nil && *cwd != "" && *cwd != Caller && !strings.HasPrefix(osutil.ExpandUser(*cwd), "/")
}
