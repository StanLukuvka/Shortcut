package cli

import (
	"os"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/approval"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/ordjson"
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
		// errors="replace" and universal newlines
		text := strings.ReplaceAll(strings.ReplaceAll(strings.ToValidUTF8(string(raw), "\uFFFD"), "\r\n", "\n"), "\r", "\n")
		out(strings.TrimRight(text, " \t\n"))
	} else {
		out("  [file missing: " + path + "]")
	}
	return nil
}

// showJSON prints one JSON object, the entry as written plus where it resolves. A broken entry still prints,
// with its error and null for what can't be worked out.
func showJSON(c *model.Context, s *textio.Streams, words []string, lk model.Lookup) error {
	out, err := showObject(c, words, lk)
	if err != nil {
		return err
	}
	text, jerr := ordjson.MarshalIndent(out)
	if jerr != nil {
		return jerr
	}
	s.Out.WriteString(string(text) + "\n")
	return nil
}

// showFields are the keys `--field` reads: the top-level keys of `show --json`, then the entry's fields marked Show.
func showFields() []string {
	keys := []string{"name", "namespace", "scope", "defined_in", "source", "hides_global", "approval", "cwd", "run_cwd", "script_path", "error"}
	for _, f := range model.Fields() {
		if f.Show {
			keys = append(keys, f.Name)
		}
	}
	return keys
}

// showField prints one field of what `show --json` describes, bare. It only reads: nothing is written or approved.
func showField(c *model.Context, s *textio.Streams, words []string, lk model.Lookup, key string) error {
	if keys := showFields(); !slices.Contains(keys, key) {
		return Die("unknown field '" + key + "' (one of: " + strings.Join(keys, ", ") + ")")
	}
	obj, err := showObject(c, words, lk)
	if err != nil {
		return err
	}
	v, ok := obj.Get(key)
	if !ok { // an entry field; absent, or a broken entry that isn't an object, prints nothing
		if entry, isObj := obj.Vals["entry"].(*ordjson.Object); isObj {
			v, _ = entry.Get(key)
		}
	}
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		_, err = s.Out.WriteString(t + "\n")
	default:
		b, merr := ordjson.Marshal(t)
		if merr != nil {
			return merr
		}
		_, err = s.Out.WriteString(string(b) + "\n")
	}
	return err
}

// showObject is the entry as written plus where it resolves. A broken entry still gives an object,
// with its error and null for what can't be worked out.
func showObject(c *model.Context, words []string, lk model.Lookup) (*ordjson.Object, error) {
	f, err := model.FindEntry(c, words, lk)
	if err != nil {
		return nil, err
	}
	if len(f.Rest) > 0 {
		return nil, model.TooManyNames(words)
	}
	g, name := f.Group, f.Name
	nullable := func(s string) ordjson.Value {
		if s == "" {
			return nil
		}
		return s
	}
	out := &ordjson.Object{}
	out.Set("name", name)
	out.Set("namespace", nullable(g.Name))
	out.Set("scope", string(g.Kind))
	out.Set("defined_in", g.DefiningFile(name))
	if g.Kind == model.KindProject {
		out.Set("source", string(g.Source(name)))
	} else {
		out.Set("source", nil)
	}
	out.Set("hides_global", f.Hides)
	out.Set("approval", string(approval.State(c, g)))
	for _, k := range []string{"cwd", "run_cwd", "script_path", "error"} {
		out.Set(k, nil)
	}
	out.Set("entry", f.Raw)
	entry, err := model.EntryFromValue(name, f.Raw)
	if err == nil {
		if entry.Cwd != nil {
			out.Set("cwd", *entry.Cwd)
		}
		out.Set("run_cwd", model.ResolveRunDir(entry.Cwd, g, c.Cwd))
		if entry.Script != nil {
			var path string
			if path, err = model.ResolveScriptPath(g.ScriptBase(), *entry.Script); err == nil {
				out.Set("script_path", path)
			}
		}
	}
	if err != nil {
		out.Set("error", err.Error())
	}
	return out, nil
}
