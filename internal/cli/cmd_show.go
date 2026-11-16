package cli

import (
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/approval"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/osutil"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

func orCaller(s *string) string {
	if s == nil || *s == "" {
		return model.Caller
	}
	return *s
}

// cmdShow prints a shortcut's definition and where it resolves.
func cmdShow(c *model.Context, s *textio.Streams, args []string) error {
	ns, err := parse("show", args)
	if err != nil {
		return err
	}
	lk := model.Lookup{GlobalOnly: ns.Bool("skipLocal")}
	if ns.Set("field") {
		if ns.Bool("json") {
			return Die("--field and --json can't be used together")
		}
		return showField(c, s, ns.List("name"), lk, ns.Str("field"))
	}
	if ns.Bool("json") {
		return showJSON(c, s, ns.List("name"), lk)
	}
	resolved, err := model.ResolveName(c, ns.List("name"), lk)
	if err != nil {
		return err
	}
	entry, group := resolved.Entry, resolved.Group
	out := func(text string) { s.Out.WriteString(text + "\n") }

	display := resolved.Display()
	out("Name:        " + display)
	scope := "Scope:       " + string(group.Kind)
	if resolved.HidesGlobal {
		scope += "  (hides your global '" + strings.Fields(display)[0] + "')"
	}
	out(scope)
	out("Defined in:  " + group.DefiningFile(entry.Name))
	if group.Kind == model.KindProject {
		out("Source:      " + string(group.Source(entry.Name)))
	}
	out("CWD:         " + orCaller(entry.Cwd))
	out("Run CWD:     " + resolved.RunCwd)
	out("Description: " + entry.Description)
	if entry.Confirm {
		out("Confirm:     yes")
	}
	if state := approval.State(c, group); state != model.StateOK {
		what := "changed"
		if state == model.StateNew {
			what = "not approved"
		}
		out("Approval:    " + what + " (run: " + group.UpdateCmd() + ")")
	}

	if entry.Inline != nil {
		out("\nInline command:\n  " + *entry.Inline)
		return nil
	}
	path, err := model.ResolveScriptPath(group.ScriptBase(), *entry.Script)
	if err != nil {
		return err
	}
	out("\nScript (" + path + "):")
	if osutil.IsFile(path) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// invalid UTF-8 becomes U+FFFD; \r\n and \r become \n
		text := strings.ReplaceAll(strings.ReplaceAll(strings.ToValidUTF8(string(raw), "\uFFFD"), "\r\n", "\n"), "\r", "\n")
		out(strings.TrimRight(text, " \t\n"))
	} else {
		out("  [file missing: " + path + "]")
	}
	return nil
}

// showTop are the top-level keys of `show --json`, in order.
func showTop() []string {
	return []string{"name", "namespace", "scope", "defined_in", "source", "hides_global", "approval", "cwd", "run_cwd", "script_path", "error"}
}

// setOpt writes null for nil, else the string. A string that is not valid UTF-8 is refused (model.ErrNotUTF8).
func setOpt(d *model.Doc, key string, val *string) error {
	if val == nil {
		d.Set(key, []byte("null"))
		return nil
	}
	return d.SetString(key, *val)
}

// showJSON prints one JSON object, the entry as written plus where it resolves.
func showJSON(c *model.Context, s *textio.Streams, words []string, lk model.Lookup) error {
	vals, entry, err := showObject(c, words, lk)
	if err != nil {
		return err
	}
	d := &model.Doc{}
	for _, k := range showTop() {
		if k == "hides_global" {
			d.Set(k, []byte(*vals[k])) // true or false
		} else if err := setOpt(d, k, vals[k]); err != nil {
			return err
		}
	}
	d.Set("entry", entry)
	text, err := d.Pretty()
	if err != nil {
		return err
	}
	_, err = s.Out.WriteString(string(text) + "\n")
	return err
}

// showFields are the keys `--field` reads: the top-level keys of `show --json`, then the entry's fields marked Show.
func showFields() []string {
	keys := showTop() // already a fresh slice
	for _, f := range model.Fields() {
		if f.Show {
			keys = append(keys, f.Name)
		}
	}
	return keys
}

// showField prints one field of what `show --json` describes, bare. It only reads: nothing is written or approved.
// A field is printed as it is, so a name that is not valid UTF-8 prints; only `show --json` refuses it.
func showField(c *model.Context, s *textio.Streams, words []string, lk model.Lookup, key string) error {
	if keys := showFields(); !slices.Contains(keys, key) {
		return Die("unknown field '" + key + "' (one of: " + strings.Join(keys, ", ") + ")")
	}
	vals, entry, err := showObject(c, words, lk)
	if err != nil {
		return err
	}
	text, top := vals[key]
	if !top { // a field of the entry as written; a broken entry that isn't an object has none
		if d, err := model.ParseDoc(entry); err == nil {
			if str, ok := d.String(key); ok {
				text = &str
			} else if raw := string(d.Raw[key]); raw != "" && raw != "null" {
				text = &raw
			}
		}
	}
	if text != nil {
		_, err = s.Out.WriteString(*text + "\n")
	}
	return err
}

// showObject is the values of showTop (nil for null) and the entry as written. A broken entry still gives them,
// with its error and nil for what can't be worked out.
func showObject(c *model.Context, words []string, lk model.Lookup) (vals map[string]*string, entry []byte, err error) {
	f, err := model.FindEntry(c, words, lk)
	if err != nil {
		return nil, nil, err
	}
	if len(f.Rest) > 0 {
		return nil, nil, model.TooManyNames(words)
	}
	g, name := f.Group, f.Name
	str := func(s string) *string { return &s }
	vals = map[string]*string{"name": &name, "scope": str(string(g.Kind)), "defined_in": str(g.DefiningFile(name)),
		"hides_global": str(strconv.FormatBool(f.Hides)), "approval": str(string(approval.State(c, g)))}
	for _, k := range []string{"namespace", "source", "cwd", "run_cwd", "script_path", "error"} {
		vals[k] = nil
	}
	if g.Name != "" {
		vals["namespace"] = &g.Name
	}
	if g.Kind == model.KindProject {
		vals["source"] = str(string(g.Source(name)))
	}
	err = f.Item.Err
	if err == nil {
		e := f.Item.Entry
		vals["cwd"] = e.Cwd
		vals["run_cwd"] = str(model.ResolveRunDir(e.Cwd, g, c.Cwd))
		if e.Script != nil {
			var path string
			if path, err = model.ResolveScriptPath(g.ScriptBase(), *e.Script); err == nil {
				vals["script_path"] = &path
			}
		}
	}
	if err != nil {
		vals["error"] = str(err.Error())
	}
	return vals, f.Item.Raw, nil
}
