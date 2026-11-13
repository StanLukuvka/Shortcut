// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-3). Don't hand-edit without checking the plan.

package approval

import (
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// ConfirmReview is confirm_review, the question after a diff. A binary file can't be shown, so taking one unseen has
// to be typed out.
func ConfirmReview(verb string, old, new map[string]string, noTerminal string) textio.Confirm {
	var unseen []string
	for path, text := range new {
		if strings.HasPrefix(text, BinaryMark) && old[path] != text {
			unseen = append(unseen, path)
		}
	}
	if len(unseen) > 0 {
		return textio.Confirm{
			Question:   "can't show " + strings.Join(sortedStrings(unseen), ", ") + ". Type binary to " + verb + " it unseen:",
			NoTerminal: noTerminal, Accept: []string{"binary"},
		}
	}
	return textio.Confirm{Question: verb + "?", NoTerminal: noTerminal}
}

func sortedStrings(s []string) []string {
	slices.SortFunc(s, func(a, b string) int {
		switch {
		case pyjson.Less(a, b):
			return -1
		case pyjson.Less(b, a):
			return 1
		}
		return 0
	})
	return s
}

// ApproveGroup is approve_group: show what changed since the last approval and ask. What is shown is what gets
// hashed. globalNames are your global shortcuts and namespaces, which a project's names hide inside it. It returns
// the exit code; an error is a die (no terminal to ask on, or the record can't be written).
func ApproveGroup(s *textio.Streams, g model.Group, assumeYes bool, globalNames map[string]bool) (int, error) {
	if g.Error != nil {
		s.Err.WriteString("error: cannot approve " + g.Label() + ": " + *g.Error + "\n")
		return 1, nil
	}
	if errs := model.EntryErrors(g); len(errs) > 0 {
		for _, e := range errs {
			s.Err.WriteString("error: cannot approve " + g.Label() + ": " + e + "\n")
		}
		return 1, nil
	}
	shared, local := SharedFiles(g), LocalFiles(g)
	files := merge(shared, local)
	if State(g) == "ok" {
		s.Out.WriteString(g.Label() + ": already approved, nothing has changed.\n")
		return 0, nil
	}
	old := map[string]string{}
	if record := Load(g); record != nil {
		old = merge(record.Snapshot, record.Local)
	}
	where := ""
	if g.Name != "" {
		where = " (" + pyos.Realpath(g.File) + ")" // the real place, wherever it points
	}
	head := "New to you:"
	if len(old) > 0 {
		head = "Changes to"
	}
	s.Out.WriteString(head + " " + g.Label() + where + "\n")
	textio.ShowDiff(s, DiffFiles(old, files))
	local2 := g.LocalShortcuts()
	for _, name := range sortedStrings(append([]string(nil), g.SharedShortcuts().Keys...)) {
		if _, both := local2.Get(name); both {
			s.Out.WriteString("'" + name + "' in " + model.ProjectFile + " is " + g.Source(name) + " by " + model.LocalFile + " for you.\n")
		}
	}
	shortcuts := g.Shortcuts()
	if g.IsProject() {
		hidden := shortcuts.Keys
		if g.Name != "" {
			hidden = []string{g.Name}
		}
		for _, name := range sortedStrings(append([]string(nil), hidden...)) {
			if globalNames[name] {
				s.Out.WriteString("Inside this project, '" + name + "' hides your global '" + name + "'.\n")
			}
		}
	}
	for _, name := range sortedStrings(append([]string(nil), shortcuts.Keys...)) {
		data, ok := shortcuts.Vals[name].(*pyjson.Object)
		if !ok {
			continue
		}
		cwd, isStr := data.Vals["cwd"].(string)
		if exported, _ := data.Vals["export"].(bool); exported && isStr && model.IsRelativeCwd(&cwd) {
			s.Err.WriteString("warning: '" + name + "' is exported, but its cwd '" + cwd + "' only exists in this checkout\n")
		}
	}
	granted, err := ConfirmReview("approve", old, files, "approve "+g.Label()+" in a terminal, or pass -y").Granted(s, assumeYes)
	if err != nil {
		return 0, err
	}
	if !granted {
		return 1, nil
	}
	if err := Save(g, shared, local); err != nil {
		return 0, err
	}
	covers := "its definitions"
	if g.Name != "" {
		covers = "its definitions and its scripts folder"
	}
	s.Out.WriteString("Approved " + g.Label() + ". This covers " + covers + ", not what they call. " +
		"Any change to them needs `" + g.UpdateCmd() + "` again.\n")
	return 0, nil
}
