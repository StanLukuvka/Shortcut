// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (G1-5). Don't hand-edit without checking the plan.

package model

// ResolvedShortcut is a name looked up: the entry, its group and where it runs.
type ResolvedShortcut struct {
	Entry       *Entry
	Group       Group
	RunCwd      string // computed execution directory
	HidesGlobal bool   // project entry with the same name as a global one
}

func (r ResolvedShortcut) Display() string { return r.Group.Display(r.Entry.Name) }
