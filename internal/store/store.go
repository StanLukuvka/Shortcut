package store

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/StanLukuvka/Shortcut/internal/platform"
)

// Store is a store's main group and its namespaces.
type Store struct {
	Main       Group
	Namespaces []NamedGroup // sorted by name
	Problems   []string     // load errors: a name that is both a shortcut and a namespace
}

type NamedGroup struct {
	Name  string
	Group Group
}

// Namespace finds an imported namespace by name.
func (s Store) Namespace(name string) (Group, bool) {
	for _, n := range s.Namespaces {
		if n.Name == name {
			return n.Group, true
		}
	}
	return Group{}, false
}

func (s Store) Groups() []Group {
	out := []Group{s.Main}
	for _, n := range s.Namespaces {
		out = append(out, n.Group)
	}
	return out
}

// ResolveRunDir returns where a shortcut with this cwd runs.
func ResolveRunDir(cwd *string, g Group, callerCwd string) string {
	if cwd == nil || *cwd == "" || *cwd == Caller {
		return callerCwd
	}
	return filepath.Clean(platform.Join(g.Folder(), platform.ExpandUser(*cwd)))
}

// LoadGroup reads one file into a Group. It never fails: why a file can't be used is on the Group.
func LoadGroup(path, storeDir, name string) Group {
	kind := KindGlobal
	if name != "" {
		kind = KindNamespace
	}
	g := Group{Kind: kind, Name: name, File: path, StoreDir: storeDir, Doc: platform.Doc{}}
	data, err := LoadRegistryFile(path)
	var missing *RegistryNotFoundError
	switch {
	case errors.As(err, &missing):
		if name != "" { // a store without a registry is simply empty
			msg := err.Error()
			g.Error = &msg
		}
	case err != nil:
		msg := err.Error()
		g.Error = &msg
	default:
		g.Doc, g.Shared = data, ItemsOf(data)
		if msg := fileError(path, data, name); msg != "" {
			g.Error = &msg
		}
	}
	return g
}

// EntryErrors finds every entry that couldn't run, without running anything.
func EntryErrors(g Group) []string {
	var errs []string
	for _, it := range g.Shortcuts() {
		if err := entryError(g, it); err != nil {
			errs = append(errs, err.Error())
		}
	}
	return errs
}

func entryError(g Group, it Item) error {
	if err := ValidateName(it.Name); err != nil {
		return err
	}
	if it.Err != nil {
		return it.Err
	}
	if e := it.Entry; e.Script != nil {
		path, err := ResolveScriptPath(g.ScriptBase(), *e.Script)
		if err != nil {
			return err
		}
		if !platform.IsFile(path) {
			return fmt.Errorf("'%s' is not a valid entry: script not found: %s", it.Name, *e.Script)
		}
	}
	return nil
}

// LoadProjectGroup is a project's .shortcuts with .shortcuts.local over it.
func LoadProjectGroup(root string) Group {
	shared := LoadGroup(platform.Join(root, ProjectFile), root, "")
	localPath := platform.Join(root, LocalFile)
	local := LoadGroup(localPath, root, "")
	g := shared
	g.Kind, g.InProject = KindProject, true
	g.LocalFile, g.LocalDoc, g.Local = localPath, local.Doc, local.Shared
	switch {
	case shared.Error != nil:
	case local.Error != nil:
		g.Error = local.Error
	default:
		if local.Doc.Has("imports") {
			msg := fmt.Sprintf("%s can't have 'imports': namespaces belong in %s", localPath, ProjectFile)
			g.Error = &msg
		}
	}
	return g
}

// GlobalNamesOf is the names your global store holds: its shortcuts and its namespaces, which a project's names hide.
func GlobalNamesOf(stores []Store) map[string]bool {
	names := map[string]bool{}
	home := stores[len(stores)-1]
	for _, k := range home.Main.Shortcuts().Names() {
		names[k] = true
	}
	for _, n := range home.Namespaces {
		names[n.Name] = true
	}
	return names
}
