package show

import (
	"os"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/terminal"
	"github.com/StanLukuvka/Shortcut/internal/terminal/argparse"
)

func orCaller(s *string) string {
	if s == nil || *s == "" {
		return store.Caller
	}
	return *s
}

// Run prints a shortcut's definition and where it resolves.
func Run(c *store.Context, s *terminal.Streams, cmd terminal.Command, args []string) error {
	ns, err := cmd.Parse(args)
	if err != nil {
		return err
	}
	scope, names := store.SplitScope(ns.List("name"))
	if len(names) == 0 {
		return argparse.UsageError("show", "missing name")
	}
	lk := store.Lookup{Scope: scope, Cmd: "show"}
	if ns.Set("field") {
		if ns.Bool("json") {
			return terminal.Die("--field and --json can't be used together")
		}
		return showField(c, s, names, lk, ns.Str("field"))
	}
	if ns.Bool("json") {
		return showJSON(c, s, names, lk)
	}
	resolved, rest, err := store.ResolveShortcut(c, names, lk)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return store.TooManyNames(names)
	}
	entry, group := resolved.Entry, resolved.Group
	out := func(text string) { s.Out.WriteString(text + "\n") }

	display := resolved.Display()
	out("Name:        " + display)
	out("Scope:       " + string(group.Kind))
	out("Defined in:  " + group.DefiningFile(entry.Name))
	if group.Kind == store.KindProject {
		out("Source:      " + string(group.Source(entry.Name)))
	}
	out("CWD:         " + orCaller(entry.Cwd))
	out("Run CWD:     " + resolved.RunCwd)
	out("Description: " + entry.Description)
	if entry.Confirm {
		out("Confirm:     yes")
	}

	if entry.Inline != nil {
		out("\nInline command:\n  " + *entry.Inline)
		return nil
	}
	path := store.ScriptPath(group.ScriptBase(), *entry.Script)
	out("\nScript (" + path + "):")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out(strings.TrimRight(string(raw), " \t\n"))
	return nil
}

// showTop are the top-level keys of `show --json`.
func showTop() []string {
	return []string{"name", "namespace", "scope", "defined_in", "source", "cwd", "run_cwd", "script_path", "error"}
}

// setOpt writes null for nil, else the string.
func setOpt(d platform.Doc, key string, val *string) {
	if val == nil {
		d[key] = []byte("null")
		return
	}
	d.SetString(key, *val)
}

// showJSON prints one JSON object, the entry plus where it resolves.
func showJSON(c *store.Context, s *terminal.Streams, words []string, lk store.Lookup) error {
	vals, entry, err := showObject(c, words, lk)
	if err != nil {
		return err
	}
	d := platform.Doc{}
	for _, k := range showTop() {
		setOpt(d, k, vals[k])
	}
	if o, ok := platform.ObjectOf(entry); ok { // an object's fields come out sorted, whatever the file's order
		if entry, err = o.Bytes(); err != nil {
			return err
		}
	}
	d["entry"] = entry
	text, err := d.Pretty()
	if err != nil {
		return err
	}
	s.Out.WriteString(string(text))
	return nil
}

// showFields are the keys `--field` reads: the top-level keys of `show --json`, then the entry's fields marked Show.
func showFields() []string {
	keys := showTop() // already a fresh slice
	for _, f := range store.Fields() {
		if f.Show {
			keys = append(keys, f.Name)
		}
	}
	return keys
}

// showField prints one field of what `show --json` describes, bare.
// A field is printed as it is, so a name that is not valid UTF-8 prints; only `show --json` refuses it.
func showField(c *store.Context, s *terminal.Streams, words []string, lk store.Lookup, key string) error {
	if !slices.Contains(showFields(), key) {
		return terminal.Die("unknown field '" + key + "'")
	}
	vals, entry, err := showObject(c, words, lk)
	if err != nil {
		return err
	}
	text, top := vals[key]
	if !top { // a field of the entry as written; a broken entry that isn't an object has none
		if d, err := platform.ParseDoc(entry); err == nil {
			if str, ok := d.String(key); ok {
				text = &str
			} else if raw := string(d[key]); raw != "" && raw != "null" {
				text = &raw
			}
		}
	}
	if text != nil {
		s.Out.WriteString(*text + "\n")
	}
	return nil
}

// showObject is the values of showTop (nil for null) and the entry as written. A broken entry still gives them,
// with its error and nil for what can't be worked out.
func showObject(c *store.Context, words []string, lk store.Lookup) (vals map[string]*string, entry []byte, err error) {
	f, err := store.FindEntry(c, words, lk)
	if err != nil {
		return nil, nil, err
	}
	if len(f.Rest) > 0 {
		return nil, nil, store.TooManyNames(words)
	}
	g, name := f.Group, f.Name
	str := func(s string) *string { return &s }
	vals = map[string]*string{"name": &name, "scope": str(string(g.Kind)), "defined_in": str(g.DefiningFile(name))}
	for _, k := range []string{"namespace", "source", "cwd", "run_cwd", "script_path", "error"} {
		vals[k] = nil
	}
	if g.Name != "" {
		vals["namespace"] = &g.Name
	}
	if g.Kind == store.KindProject {
		vals["source"] = str(string(g.Source(name)))
	}
	err = f.Item.Err
	if err == nil {
		e := f.Item.Entry
		vals["cwd"] = e.Cwd
		var runCwd string
		if runCwd, err = store.ResolveRunDir(e.Cwd, g, c.Cwd); err == nil {
			vals["run_cwd"] = &runCwd
		}
		if err == nil && e.Script != nil {
			path := store.ScriptPath(g.ScriptBase(), *e.Script)
			vals["script_path"] = &path
		}
	}
	if err != nil {
		vals["error"] = str(err.Error())
	}
	return vals, f.Item.Raw, nil
}
