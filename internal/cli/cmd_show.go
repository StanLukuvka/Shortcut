// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-2). Don't hand-edit without checking the plan.

package cli

import (
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/approval"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// isPySpace is str.isspace() for one character: Python counts U+001C..U+001F as white space too.
func isPySpace(r rune) bool { return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) }

// pyRstrip is str.rstrip().
func pyRstrip(s string) string { return strings.TrimRightFunc(s, isPySpace) }

// replaceInvalidUTF8 is bytes.decode("utf-8", "replace"): one U+FFFD for each maximal invalid subpart, so a
// truncated sequence is one and each stray byte is one (strings.ToValidUTF8 would merge a run into one).
func replaceInvalidUTF8(raw []byte) string {
	var b strings.Builder
	for i := 0; i < len(raw); {
		r, n := utf8.DecodeRune(raw[i:])
		if r != utf8.RuneError || n != 1 {
			b.WriteString(string(raw[i : i+n]))
			i += n
			continue
		}
		// a lead byte followed by the continuation bytes it is allowed: that much is one bad sequence
		lo, hi, need := byte(0x80), byte(0xBF), 0
		switch c := raw[i]; {
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c >= 0xE0 && c <= 0xEF:
			need = 2
			if c == 0xE0 {
				lo = 0xA0
			} else if c == 0xED {
				hi = 0x9F
			}
		case c >= 0xF0 && c <= 0xF4:
			need = 3
			if c == 0xF0 {
				lo = 0x90
			} else if c == 0xF4 {
				hi = 0x8F
			}
		}
		j := i + 1
		for k := 0; k < need && j < len(raw) && raw[j] >= lo && raw[j] <= hi; k++ {
			j++
			lo, hi = 0x80, 0xBF
		}
		b.WriteString("\uFFFD")
		i = j
	}
	return b.String()
}

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

// cmdShow is cmd_show.
func cmdShow(s *textio.Streams, args []string) error {
	ns, err := parsers["show"].Parse(args)
	if err != nil {
		return err
	}
	if ns.Bool("json") {
		return showJSON(s, ns.List("name"), ns.Bool("skip_local"))
	}
	resolved, err := model.ResolveName(ns.List("name"), ns.Bool("skip_local"))
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
	if entry.Created != nil && *entry.Created != "" {
		out("Created:     " + *entry.Created)
	}
	if entry.CreatedBy != nil && *entry.CreatedBy != "" {
		out("Created by:  " + *entry.CreatedBy)
	}
	if entry.Icon != nil && *entry.Icon != "" {
		out("Icon:        " + *entry.Icon)
	}
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
	if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
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

// showJSON is show_json: one JSON object, the entry as written plus where it resolves. A broken entry still prints,
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
	s.Out.WriteString(pyjson.Dumps(out, pyjson.Opts{Indent: 2, EnsureASCII: true}) + "\n")
	return nil
}
