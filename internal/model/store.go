package model

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/StanLukuvka/Shortcut/internal/ordjson"
	"github.com/StanLukuvka/Shortcut/internal/osutil"
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
	return filepath.Clean(osutil.Join(g.Folder(), osutil.ExpandUser(*cwd)))
}

// LoadGroup reads one file into a Group. It never fails: why a file can't be used is on the Group.
func LoadGroup(path, storeDir, name string) Group {
	g := Group{Name: name, File: path, StoreDir: storeDir, Data: &ordjson.Object{}}
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
		g.Data = data
		if msg := fileError(path, data, name); msg != "" {
			g.Error = &msg
		}
	}
	return g
}

// EntryErrors finds every entry that couldn't run, without running anything.
func EntryErrors(g Group) []string {
	var errs []string
	shortcuts := g.Shortcuts()
	for _, name := range shortcuts.Keys {
		if err := entryError(g, name, shortcuts.Vals[name]); err != nil {
			errs = append(errs, err.Error())
		}
	}
	return errs
}

func entryError(g Group, name string, data ordjson.Value) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	e, err := EntryFromValue(name, data)
	if err != nil {
		return err
	}
	if e.Script != nil {
		path, err := ResolveScriptPath(g.ScriptBase(), *e.Script)
		if err != nil {
			return err
		}
		if !osutil.IsFile(path) {
			return fmt.Errorf("'%s' is not a valid entry: script not found: %s", name, *e.Script)
		}
	}
	return nil
}

// LoadProjectGroup is a project's .shortcuts with .shortcuts.local over it.
func LoadProjectGroup(root string) Group {
	shared := LoadGroup(osutil.Join(root, ProjectFile), root, "")
	localPath := osutil.Join(root, LocalFile)
	local := LoadGroup(localPath, root, "")
	g := shared
	g.LocalFile, g.LocalData = localPath, local.Data
	switch {
	case shared.Error != nil:
	case local.Error != nil:
		g.Error = local.Error
	default:
		if _, has := local.Data.Get("imports"); has {
			msg := fmt.Sprintf("%s can't have 'imports': namespaces belong in %s", localPath, ProjectFile)
			g.Error = &msg
		}
	}
	return g
}
