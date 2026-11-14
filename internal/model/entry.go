// Package model holds shortcut's data: entries, groups, stores, and the files they are read from and written to.
// It never prints and never imports cli.
package model

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
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

// Origin is "from" in JSON: the project promote took an entry from. Global shortcuts.json only.
type Origin struct{ Source, Name string }

// Entry is one shortcut's definition.
type Entry struct {
	Name                          string
	Description                   string  // "" allowed; always written
	Inline                        *string // exactly one of Inline and Script is non-nil
	Script                        *string
	Cwd, Icon, CreatedBy, Created *string
	Confirm, Export               bool
	Args                          []*pyjson.Object // nil = field absent; kept raw so key order round-trips
	Origin                        *Origin
}

var knownFields = map[string]bool{
	"description": true, "inline": true, "script": true, "cwd": true, "icon": true, "created_by": true,
	"created": true, "confirm": true, "export": true, "args": true, "from": true,
}

var textFields = []string{"inline", "script", "cwd", "icon", "created_by", "created"}

// EntryFromValue builds an Entry from a decoded value, with fixed messages and check order.
func EntryFromValue(name string, v pyjson.Value) (*Entry, error) {
	bad := func(problem string) error { return fmt.Errorf("'%s' is not a valid entry: %s", name, problem) }
	data, ok := v.(*pyjson.Object)
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
	flags := map[string]bool{}
	for _, f := range []string{"confirm", "export"} {
		if val, present := data.Get(f); present {
			b, isBool := val.(bool)
			if !isBool {
				return nil, bad(fmt.Sprintf("'%s' must be a boolean", f))
			}
			flags[f] = b
		}
	}
	if (texts["inline"] == nil) == (texts["script"] == nil) {
		return nil, bad("it needs exactly one of 'inline' and 'script'")
	}
	if args, present := data.Get("args"); present {
		if err := ValidateArgs(name, args); err != nil {
			return nil, err
		}
		list := args.([]pyjson.Value)
		e.Args = make([]*pyjson.Object, len(list))
		for i, a := range list {
			e.Args[i] = a.(*pyjson.Object)
		}
	}
	if o, present := data.Get("from"); present {
		obj, isObj := o.(*pyjson.Object)
		var src, nm string
		okFrom := isObj && len(obj.Keys) == 2
		if okFrom {
			s, ok1 := obj.Vals["source"].(string)
			n, ok2 := obj.Vals["name"].(string)
			src, nm, okFrom = s, n, ok1 && ok2
		}
		if !okFrom {
			return nil, bad("'from' must be an object with a string 'source' and 'name'")
		}
		e.Origin = &Origin{Source: src, Name: nm}
	}
	e.Inline, e.Script, e.Cwd, e.Icon, e.CreatedBy, e.Created =
		texts["inline"], texts["script"], texts["cwd"], texts["icon"], texts["created_by"], texts["created"]
	e.Confirm, e.Export = flags["confirm"], flags["export"]
	return e, nil
}

// ToObject returns the entry as an object: description, then the text fields that are set, confirm, export, args, from.
func (e *Entry) ToObject() *pyjson.Object {
	o := &pyjson.Object{}
	o.Set("description", e.Description)
	for i, p := range []*string{e.Inline, e.Script, e.Cwd, e.Icon, e.CreatedBy, e.Created} {
		if p != nil {
			o.Set(textFields[i], *p)
		}
	}
	if e.Confirm {
		o.Set("confirm", true)
	}
	if e.Export {
		o.Set("export", true)
	}
	if e.Args != nil {
		list := make([]pyjson.Value, len(e.Args))
		for i, a := range e.Args {
			list[i] = a
		}
		o.Set("args", list)
	}
	if e.Origin != nil {
		from := &pyjson.Object{}
		from.Set("source", e.Origin.Source)
		from.Set("name", e.Origin.Name)
		o.Set("from", from)
	}
	return o
}

var argName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// ArgVar is the environment variable a declared argument is in.
func ArgVar(name string) string {
	return "SC_ARG_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// ValidateArgs checks a declared args list, with fixed messages and check order.
func ValidateArgs(name string, args pyjson.Value) error {
	bad := func(msg string) error { return fmt.Errorf("'%s' is not a valid entry: %s", name, msg) }
	list, ok := args.([]pyjson.Value)
	if !ok {
		return bad("'args' must be a list")
	}
	for i, item := range list {
		arg, isObj := item.(*pyjson.Object)
		var argN string
		if isObj {
			n, _ := arg.Get("name")
			argN, ok = n.(string)
			ok = ok && argName.MatchString(argN)
		}
		if !isObj || !ok {
			return bad("every entry in 'args' needs a 'name' (letters, digits, _ and -)")
		}
		var unknown []string
		for _, k := range arg.Keys {
			switch k {
			case "name", "required", "default", "desc", "rest":
			default:
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			return bad("unknown args field: " + strings.Join(slices.Sorted(slices.Values(unknown)), ", "))
		}
		for _, f := range []string{"default", "desc"} {
			if v, present := arg.Get(f); present {
				if _, isStr := v.(string); !isStr {
					return bad(fmt.Sprintf("args '%s' must be a string", f))
				}
			}
		}
		flag := map[string]bool{}
		for _, f := range []string{"required", "rest"} {
			if v, present := arg.Get(f); present {
				b, isBool := v.(bool)
				if !isBool {
					return bad(fmt.Sprintf("args '%s' must be a boolean", f))
				}
				flag[f] = b
			}
		}
		if flag["rest"] && i != len(list)-1 {
			return bad("only the last entry in 'args' can be 'rest'")
		}
		if _, hasDefault := arg.Get("default"); flag["required"] && hasDefault {
			return bad(fmt.Sprintf("arg '%s' can't be both 'required' and have a 'default'", argN))
		}
		for _, other := range list[:i] {
			if n, _ := other.(*pyjson.Object).Get("name"); n == argN {
				return bad(fmt.Sprintf("arg '%s' is declared twice", argN))
			}
		}
		for _, other := range list[:i] {
			on, _ := other.(*pyjson.Object).Get("name")
			if ArgVar(on.(string)) == ArgVar(argN) {
				return bad(fmt.Sprintf("args '%s' and '%s' would both be %s", on, argN, ArgVar(argN)))
			}
		}
	}
	return nil
}

// IsRelativeCwd: a cwd that means a folder next to the file defining it, so it's only right in that checkout.
func IsRelativeCwd(cwd *string) bool {
	return cwd != nil && *cwd != "" && *cwd != Caller && !strings.HasPrefix(pyos.ExpandUser(*cwd), "/")
}

// PlacementError reports fields only some files can have ('export' a project's own, 'from' the global
// store's shortcuts.json). "" = none.
func PlacementError(e *Entry, inProject, inGlobal bool) string {
	if e.Export && !inProject {
		return fmt.Sprintf("'%s' is not a valid entry: 'export' is only for %s and %s", e.Name, ProjectFile, LocalFile)
	}
	if e.Origin != nil && !inGlobal {
		return fmt.Sprintf("'%s' is not a valid entry: 'from' is only for the global store's shortcuts.json", e.Name)
	}
	return ""
}
