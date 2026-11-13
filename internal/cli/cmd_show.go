package cli

import (
	"os"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/approval"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

func scopeOf(g model.Group) string {
	switch {
	case g.Name != "":
		return "namespace"
	case g.NeedsApproval():
		return "project"
	}
	return "global"
}

func orCaller(s *string) string {
	if s == nil || *s == "" {
		return model.Caller
	}
	return *s
}

// cmdShow prints a shortcut's definition and where it resolves.
func cmdShow(s *textio.Streams, args []string) error {
	ns, err := parsers["show"].Parse(args)
	if err != nil {
		return err
	}
	if ns.Bool("json") {
		return showJSON(s, ns.List("name"), ns.Bool("skipLocal"))
	}
	resolved, err := model.ResolveName(ns.List("name"), ns.Bool("skipLocal"))
	if err != nil {
		return err
	}
	entry, group := resolved.Entry, resolved.Group
	out := func(text string) { s.Out.WriteString(text + "\n") }

	display := resolved.Display()
	out("Name:        " + display)
	scope := "Scope:       " + scopeOf(group)
	if resolved.HidesGlobal {
		scope += "  (hides your global '" + strings.Fields(display)[0] + "')"
	}
	out(scope)
	out("Defined in:  " + group.DefiningFile(entry.Name))
	if group.LocalFile != "" {
		out("Source:      " + group.Source(entry.Name))
	}
	out("CWD:         " + orCaller(entry.Cwd))
	out("Run CWD:     " + resolved.RunCwd)
	out("Description: " + entry.Description)
	if entry.Args != nil {
		out("Usage:       " + usageLine(display, entry.Args))
		if len(entry.Args) > 0 {
			out("Arguments:\n" + strings.Join(argLines(entry.Args), "\n"))
		}
	}
	if entry.Confirm {
		out("Confirm:     True")
	}
	if entry.Export {
		out("Export:      True")
	}
	if entry.Origin != nil {
		out("From:        '" + entry.Origin.Name + "' in " + entry.Origin.Source)
	}
	optLine := func(label string, p *string) {
		if p != nil && *p != "" {
			out(label + *p)
		}
	}
	optLine("Created:     ", entry.Created)
	optLine("Created by:  ", entry.CreatedBy)
	optLine("Icon:        ", entry.Icon)
	if state := approval.State(group); state != "ok" {
		what := "changed"
		if state == "new" {
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
	if pyos.IsFile(path) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err // lazy: Python's "[Errno 13] ..." text is not reproduced
		}
		// errors="replace" and universal newlines
		text := strings.ReplaceAll(strings.ReplaceAll(replaceInvalidUTF8(raw), "\r\n", "\n"), "\r", "\n")
		out(pyRstrip(text))
	} else {
		out("  [file missing: " + path + "]")
	}
	return nil
}

// showJSON prints one JSON object, the entry as written plus where it resolves. A broken entry still prints,
// with its error and null for what can't be worked out.
func showJSON(s *textio.Streams, words []string, skipLocal bool) error {
	f, err := model.FindEntry(words, "", skipLocal, false)
	if err != nil {
		return err
	}
	if len(f.Rest) > 0 {
		return model.TooManyNames(words)
	}
	g, name := f.Group, f.Name
	nullable := func(s string) pyjson.Value {
		if s == "" {
			return nil
		}
		return s
	}
	out := &pyjson.Object{}
	out.Set("name", name)
	out.Set("namespace", nullable(g.Name))
	out.Set("scope", scopeOf(g))
	out.Set("defined_in", g.DefiningFile(name))
	if g.LocalFile != "" {
		out.Set("source", g.Source(name))
	} else {
		out.Set("source", nil)
	}
	out.Set("hides_global", f.Hides)
	out.Set("approval", approval.State(g))
	for _, k := range []string{"cwd", "run_cwd", "script_path", "error"} {
		out.Set(k, nil)
	}
	out.Set("entry", f.Raw)
	entry, err := model.LoadEntry(g, name, f.Raw)
	if err == nil {
		if entry.Cwd != nil {
			out.Set("cwd", *entry.Cwd)
		}
		cwd, _ := pyos.Getwd()
		out.Set("run_cwd", model.ResolveRunDir(entry.Cwd, g, cwd))
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
	text, jerr := pyjson.MarshalIndent(out)
	if jerr != nil {
		return jerr
	}
	s.Out.WriteString(string(text) + "\n")
	return nil
}
