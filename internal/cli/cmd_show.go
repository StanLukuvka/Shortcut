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
		// errors="replace" and universal newlines
		text := strings.ReplaceAll(strings.ReplaceAll(strings.ToValidUTF8(string(raw), "\uFFFD"), "\r\n", "\n"), "\r", "\n")
		out(strings.TrimRight(text, " \t\n"))
	} else {
		out("  [file missing: " + path + "]")
	}
	return nil
}

// showView is what `show` describes: the entry as written and where it resolves. A broken entry still has one,
// with its error and nil for what can't be worked out.
type showView struct {
	name, scope, definedIn, approval string
	namespace, source                *string
	hidesGlobal                      bool
	cwd, runCwd, scriptPath, errText *string
	entry                            []byte // the entry as written
}

// viewField is one top-level key of `show --json`, in order. val is nil for null; isBool says val is true or false.
type viewField struct {
	key    string
	val    *string
	isBool bool
}

func (v showView) fields() []viewField {
	return []viewField{{"name", &v.name, false}, {"namespace", v.namespace, false}, {"scope", &v.scope, false},
		{"defined_in", &v.definedIn, false}, {"source", v.source, false},
		{"hides_global", ptr(strconv.FormatBool(v.hidesGlobal)), true}, {"approval", &v.approval, false},
		{"cwd", v.cwd, false}, {"run_cwd", v.runCwd, false}, {"script_path", v.scriptPath, false}, {"error", v.errText, false}}
}

func ptr(s string) *string { return &s }

// doc is the view as one object. A string that is not valid UTF-8 is refused (model.ErrNotUTF8).
func (v showView) doc() (*model.Doc, error) {
	d := &model.Doc{}
	for _, f := range v.fields() {
		switch {
		case f.val == nil:
			d.Set(f.key, []byte("null"))
		case f.isBool:
			d.Set(f.key, []byte(*f.val))
		default:
			if err := d.SetString(f.key, *f.val); err != nil {
				return nil, err
			}
		}
	}
	d.Set("entry", v.entry)
	return d, nil
}

// field is the text `--field key` prints, nil for nothing: a top-level key, else a field of the entry as written.
func (v showView) field(key string) *string {
	for _, f := range v.fields() {
		if f.key == key {
			return f.val
		}
	}
	entry, err := model.ParseDoc(v.entry)
	if err != nil { // a broken entry that isn't an object
		return nil
	}
	if s, ok := entry.String(key); ok {
		return &s
	}
	if raw := string(entry.Raw[key]); raw != "" && raw != "null" {
		return &raw
	}
	return nil
}

// showJSON prints one JSON object, the entry as written plus where it resolves.
func showJSON(c *model.Context, s *textio.Streams, words []string, lk model.Lookup) error {
	v, err := showObject(c, words, lk)
	if err != nil {
		return err
	}
	d, err := v.doc()
	if err != nil {
		return err
	}
	text, err := d.Pretty()
	if err != nil {
		return err
	}
	_, err = s.Out.WriteString(string(text) + "\n")
	return err
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
// A field is printed as it is, so a name that is not valid UTF-8 prints; only `show --json` refuses it.
func showField(c *model.Context, s *textio.Streams, words []string, lk model.Lookup, key string) error {
	if keys := showFields(); !slices.Contains(keys, key) {
		return Die("unknown field '" + key + "' (one of: " + strings.Join(keys, ", ") + ")")
	}
	v, err := showObject(c, words, lk)
	if err != nil {
		return err
	}
	if text := v.field(key); text != nil {
		_, err = s.Out.WriteString(*text + "\n")
	}
	return err
}

func showObject(c *model.Context, words []string, lk model.Lookup) (showView, error) {
	f, err := model.FindEntry(c, words, lk)
	if err != nil {
		return showView{}, err
	}
	if len(f.Rest) > 0 {
		return showView{}, model.TooManyNames(words)
	}
	g, name := f.Group, f.Name
	nullable := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	v := showView{name: name, namespace: nullable(g.Name), scope: string(g.Kind), definedIn: g.DefiningFile(name),
		hidesGlobal: f.Hides, approval: string(approval.State(c, g)), entry: f.Item.Raw}
	if g.Kind == model.KindProject {
		v.source = ptr(string(g.Source(name)))
	}
	err = f.Item.Err
	if err == nil {
		entry := f.Item.Entry
		v.cwd = entry.Cwd
		v.runCwd = ptr(model.ResolveRunDir(entry.Cwd, g, c.Cwd))
		if entry.Script != nil {
			var path string
			if path, err = model.ResolveScriptPath(g.ScriptBase(), *entry.Script); err == nil {
				v.scriptPath = &path
			}
		}
	}
	if err != nil {
		v.errText = ptr(err.Error())
	}
	return v, nil
}
