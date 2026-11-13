package model

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
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
	return filepath.Clean(pyos.Join(g.Folder(), pyos.ExpandUser(*cwd)))
}

// LoadEntry is an entry as its group allows it: EntryFromValue, plus where 'export' and 'from' can be.
func LoadEntry(g Group, name string, v pyjson.Value) (*Entry, error) {
	e, err := EntryFromValue(name, v)
	if err != nil {
		return nil, err
	}
	if msg := PlacementError(e, g.LocalFile != "", g.Name == "" && !g.IsProject()); msg != "" {
		return nil, errors.New(msg)
	}
	return e, nil
}

// LoadGroup reads one file into a Group. It never fails: why a file can't be used is on the Group.
func LoadGroup(path, storeDir, name string) Group {
	g := Group{Name: name, File: path, StoreDir: storeDir, Data: &pyjson.Object{}}
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

func entryError(g Group, name string, data pyjson.Value) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	e, err := LoadEntry(g, name, data)
	if err != nil {
		return err
	}
	if e.Script != nil {
		path, err := ResolveScriptPath(g.ScriptBase(), *e.Script)
		if err != nil {
			return err
		}
		if !pyos.IsFile(path) {
			return fmt.Errorf("'%s' is not a valid entry: script not found: %s", name, *e.Script)
		}
	}
	return nil
}

// LoadProjectGroup is a project's .shortcuts with .shortcuts.local over it.
func LoadProjectGroup(root string) Group {
	shared := LoadGroup(pyos.Join(root, ProjectFile), root, "")
	localPath := pyos.Join(root, LocalFile)
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

// LoadStore reads a store and its namespaces. It never fails on bad content: that is reported on the Group or the
// Store, so list, show and rm still work on a broken store.
// lazy: a folder that can't be listed counts as empty; no test reaches it.
func LoadStore(storeDir string) Store {
	isGlobal := IsGlobalStore(storeDir)
	var main Group
	if isGlobal {
		main = LoadGroup(pyos.Join(storeDir, "shortcuts.json"), storeDir, "")
	} else {
		main = LoadProjectGroup(storeDir)
	}
	var problems []string
	sources := map[string]string{}
	importsDir := pyos.Join(storeDir, "imports")
	if isGlobal && pyos.IsDir(importsDir) {
		entries, _ := os.ReadDir(importsDir)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		for _, filename := range slices.Sorted(slices.Values(names)) {
			if strings.HasSuffix(filename, ImportSuffix) {
				sources[strings.TrimSuffix(filename, ImportSuffix)] = pyos.Join(importsDir, filename)
			}
		}
	}
	if imports, ok := main.Data.Vals["imports"].(*pyjson.Object); ok {
		for _, name := range imports.Keys {
			path, isStr := imports.Vals[name].(string)
			_, taken := sources[name]
			switch {
			case !isStr:
				problems = append(problems, fmt.Sprintf("import '%s' in %s must be a path", name, main.File))
			case !strings.HasSuffix(path, ImportSuffix):
				problems = append(problems, fmt.Sprintf("import '%s' in %s must be a %s file", name, main.File, ImportSuffix))
			case filepath.Base(path) == ImportSuffix:
				problems = append(problems, fmt.Sprintf("import '%s' in %s: %s", name, main.File, bareImport))
			case taken:
				problems = append(problems, fmt.Sprintf("namespace '%s' is imported twice in %s", name, storeDir))
			default:
				sources[name] = filepath.Clean(pyos.Join(main.Folder(), pyos.ExpandUser(path)))
			}
		}
	}
	names := make([]string, 0, len(sources))
	for n := range sources {
		names = append(names, n)
	}
	var namespaces []NamedGroup
	mainShortcuts := main.Shortcuts()
	for _, name := range slices.Sorted(slices.Values(names)) {
		path := sources[name]
		g := LoadGroup(path, storeDir, name)
		if err := ValidateName(name); err != nil {
			msg := err.Error()
			if name == "" {
				msg = fmt.Sprintf("%s: %s", path, bareImport)
			}
			g.Error = &msg
		}
		namespaces = append(namespaces, NamedGroup{name, g})
		if _, clash := mainShortcuts.Get(name); clash {
			problems = append(problems, fmt.Sprintf("'%s' is both a shortcut and a namespace in %s; rename one", name, storeDir))
		}
	}
	return Store{Main: main, Namespaces: namespaces, Problems: problems}
}

// VisibleStores are the stores a name is looked up in, nearest first.
func VisibleStores(callerCwd string, skipLocal bool) []Store {
	var dirs []string
	if !skipLocal {
		if root := FindProjectRoot(callerCwd); root != "" {
			dirs = append(dirs, root)
		}
	}
	dirs = append(dirs, GlobalStoreDir())
	stores := make([]Store, len(dirs))
	for i, d := range dirs {
		stores[i] = LoadStore(d)
	}
	return stores
}

// ReloadGroup reads a group's file(s) again.
func ReloadGroup(g Group) Group {
	if g.LocalFile != "" {
		return LoadProjectGroup(g.StoreDir)
	}
	return LoadGroup(g.File, g.StoreDir, g.Name)
}
