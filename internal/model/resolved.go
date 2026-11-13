package model

// ResolvedShortcut is a name looked up: the entry, its group and where it runs.
type ResolvedShortcut struct {
	Entry       *Entry
	Group       Group
	RunCwd      string // computed execution directory
	HidesGlobal bool   // project entry with the same name as a global one
}

func (r ResolvedShortcut) Display() string { return r.Group.Display(r.Entry.Name) }
