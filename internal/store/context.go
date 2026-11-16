package store

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/platform"
)

// Context is what a run knows about where it is: built once per run, passed to every command and to every
// store function that needs the cwd, the global store or the project root. One Context is one run:
// the stores it has read are not read again, except by ReadStore.
type Context struct {
	Cwd     string // physical working directory; "" when CwdErr != nil
	CwdErr  error  // read by run and new, which refuse to go on without a cwd; also set when the project lookup failed
	Global  string // absolute global store dir: $SHORTCUTS_HOME or ~/.shortcuts. The only read of the variable
	Project string // nearest project root at or above Cwd, "" = none

	globalReal string           // platform.Realpath(Global), once
	stores     map[string]Store // by the dir as the caller passed it: Global, or a realpath'd project root
}

// NewContext resolves the global store and the project root once. It never fails: a project lookup that can't tell
// is CwdErr.
func NewContext(cwd string, cwdErr error) *Context {
	home := os.Getenv("SHORTCUTS_HOME")
	if home == "" {
		home = "~/.shortcuts"
	}
	c := &Context{Cwd: cwd, CwdErr: cwdErr, Global: platform.Abs(platform.ExpandUser(home)), stores: map[string]Store{}}
	c.globalReal = platform.Realpath(c.Global)
	if cwdErr == nil {
		c.Project, c.CwdErr = c.FindProjectRoot(cwd)
	}
	return c
}

// IsGlobal reports whether dir is the global store, after resolving symlinks in both.
func (c *Context) IsGlobal(dir string) bool { return platform.Realpath(dir) == c.globalReal }

// neverAProject: ~/.shortcuts is never a project, also when SHORTCUTS_HOME points the global store elsewhere.
func (c *Context) neverAProject(p string) bool {
	return p == c.globalReal || p == platform.Realpath(platform.Join(platform.ExpandUser("~"), ProjectFile))
}

func (c *Context) isProjectRoot(folder string) (bool, error) {
	if c.neverAProject(platform.Realpath(platform.Join(folder, ProjectFile))) {
		return false, nil
	}
	for _, name := range []string{ProjectFile, LocalFile} {
		if ok, err := platform.IsFile(platform.Join(folder, name)); ok || err != nil {
			return ok, err
		}
	}
	return false, nil
}

// FindProjectRoot returns the nearest folder at or above start with a .shortcuts or .shortcuts.local that is
// not the global store. "" = none. A folder it can't look in is an error, not "no project".
func (c *Context) FindProjectRoot(start string) (string, error) {
	for cur := platform.Realpath(start); ; {
		ok, err := c.isProjectRoot(cur)
		if err != nil {
			return "", err
		}
		if ok {
			return cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", nil
		}
		cur = parent
	}
}

// LocalProjectRoot returns where --local writes, the cwd and never a parent. Its errors are
// the messages die prints.
func (c *Context) LocalProjectRoot() (string, error) {
	here := platform.Realpath(c.Cwd)
	root, err := c.FindProjectRoot(here)
	if err != nil {
		return "", err
	}
	if root != "" && root != here {
		return "", fmt.Errorf("%s is inside the project at %s. Run this from there.", here, root)
	}
	if c.neverAProject(platform.Realpath(platform.Join(here, ProjectFile))) {
		return "", fmt.Errorf("%s holds the global store, so it can't hold local shortcuts. Run this inside a project folder.", here)
	}
	if platform.IsRelativeTo(here, c.globalReal) {
		return "", fmt.Errorf("%s is inside the global store, so it can't hold local shortcuts. Run this inside a project folder.", here)
	}
	if st, err := os.Lstat(platform.Join(here, LocalFile)); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is a symlink, so writing it would go somewhere else.", platform.Join(here, LocalFile))
	}
	return here, nil
}

// WithLock runs fn holding the one lock file, <global store>/.lock, which every write takes: the registry, a
// namespace file and .shortcuts.local. Not reentrant: never nest it.
func (c *Context) WithLock(fn func() error) error {
	return platform.WithLock(platform.Join(c.Global, ".lock"), fn)
}

// Store is the store at dir, read the first time and not again in this run.
func (c *Context) Store(dir string) Store {
	st, ok := c.stores[dir]
	if !ok {
		st = c.loadStore(dir)
		c.stores[dir] = st
	}
	return st
}

// ReadStore reads the store at dir from disk, not through the cache. For a check made under a registry lock,
// which must see what another process wrote.
func (c *Context) ReadStore(dir string) Store { return c.loadStore(dir) }

// Stores are the project's store, if there is one, then the global one: what list shows.
func (c *Context) Stores() []Store {
	var stores []Store
	if c.Project != "" {
		stores = append(stores, c.Store(c.Project))
	}
	return append(stores, c.Store(c.Global))
}

// loadStore reads a store and its namespaces. It never fails on bad content: that is reported on the Group or the
// Store, so list, show and rm still work on a broken store.
func (c *Context) loadStore(storeDir string) Store {
	isGlobal := c.IsGlobal(storeDir)
	var main Group
	if isGlobal {
		main = LoadGroup(platform.Join(storeDir, "shortcuts.json"), storeDir, "")
	} else {
		main = LoadProjectGroup(storeDir)
	}
	var problems []string
	sources := map[string]string{}
	importsDir := platform.Join(storeDir, "imports")
	if isGlobal {
		isDir, err := platform.IsDir(importsDir)
		if err != nil {
			problems = append(problems, err.Error())
		}
		if isDir {
			entries, err := os.ReadDir(importsDir)
			if err != nil {
				problems = append(problems, err.Error())
			}
			for _, e := range entries {
				if filename := e.Name(); strings.HasSuffix(filename, ImportSuffix) {
					sources[strings.TrimSuffix(filename, ImportSuffix)] = platform.Join(importsDir, filename)
				}
			}
		}
	}
	if imports, ok := main.Doc.Object("imports"); ok {
		for _, name := range slices.Sorted(maps.Keys(imports)) {
			path, isStr := imports.String(name)
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
				sources[name] = filepath.Clean(platform.Join(main.Folder(), platform.ExpandUser(path)))
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
		g.InProject = !isGlobal
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
