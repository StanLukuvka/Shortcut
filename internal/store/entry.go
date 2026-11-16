// Package store holds shortcut's data: entries, groups, stores, and the files they are read from and written to.
// It never prints and never imports commands or term.
package store

import (
	"fmt"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/platform"
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

// Fields are the entry's fields, in the order they are checked. Validation, Raw and
// `show --field` read them, and a test checks docs/shortcut.schema.json against them.
func Fields() []Field {
	return []Field{
		{"description", "string", true}, {"inline", "string", true}, {"script", "string", true},
		{"cwd", "string", false}, {"confirm", "boolean", false},
	}
}

// DecodeEntry reads an entry's text, with fixed messages and check order. For an object it returns the entry
// with every well-typed field set, even beside an error, so approval still sees a broken entry's script and cwd.
func DecodeEntry(name string, raw []byte) (*Entry, error) {
	bad := func(problem string) error { return fmt.Errorf("'%s' is not a valid entry: %s", name, problem) }
	data, ok := platform.ObjectOf(raw)
	if !ok {
		return nil, bad("expected a JSON object")
	}
	// An ignored field could be a safety setting from a newer version, so never ignore one
	fields := Fields()
	var unknown []string
	for k := range data {
		if !slices.ContainsFunc(fields, func(f Field) bool { return f.Name == k }) {
			unknown = append(unknown, k)
		}
	}
	problem := ""
	if len(unknown) > 0 {
		problem = "unknown field: " + strings.Join(slices.Sorted(slices.Values(unknown)), ", ")
	}
	e := &Entry{Name: name}
	for _, f := range fields {
		if !data.Has(f.Name) {
			continue
		}
		s, isString := data.String(f.Name)
		isTrue := string(data[f.Name]) == "true"
		typed := isString
		if f.Type == "boolean" {
			typed = isTrue || string(data[f.Name]) == "false"
		}
		switch {
		case !typed:
			if problem == "" {
				problem = "'" + f.Name + "' must be a " + f.Type
			}
		case f.Name == "description":
			e.Description = s
		case f.Name == "inline":
			e.Inline = &s
		case f.Name == "script":
			e.Script = &s
		case f.Name == "cwd":
			e.Cwd = &s
		case f.Name == "confirm":
			e.Confirm = isTrue
		}
	}
	if problem == "" && (e.Inline == nil) == (e.Script == nil) {
		problem = "it needs exactly one of 'inline' and 'script'"
	}
	if problem != "" {
		return e, bad(problem)
	}
	return e, nil
}

// Raw is the entry as an object's text, its fields sorted by name: description always, the others when set.
func (e *Entry) Raw() ([]byte, error) {
	text := map[string]*string{"inline": e.Inline, "script": e.Script, "cwd": e.Cwd}
	d := platform.Doc{}
	for _, f := range Fields() {
		switch {
		case f.Name == "description":
			if err := d.SetString(f.Name, e.Description); err != nil {
				return nil, err
			}
		case f.Name == "confirm":
			if e.Confirm {
				d[f.Name] = []byte("true")
			}
		case text[f.Name] != nil:
			if err := d.SetString(f.Name, *text[f.Name]); err != nil {
				return nil, err
			}
		}
	}
	return d.Bytes()
}

// IsRelativeCwd: a cwd that means a folder next to the file defining it, so it's only right in that checkout.
func IsRelativeCwd(cwd *string) bool {
	return cwd != nil && *cwd != "" && *cwd != Caller && !strings.HasPrefix(platform.ExpandUser(*cwd), "/")
}
