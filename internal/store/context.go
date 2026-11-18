package store

import (
	"errors"
	"fmt"
	"io/fs"
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
	Cwd        string // physical working directory; "" when CwdErr != nil
	CwdErr     error  // getcwd's error, read by run and new, which refuse to go on without a cwd
	Global     string // absolute global store dir: $SHORTCUTS_HOME or ~/.shortcuts. The only read of the variable
	GlobalErr  error  // $SHORTCUTS_HOME or ~ can't be used, or the store can't be resolved: no command runs
	Project    string // nearest project root at or above Cwd, "" = none
	ProjectErr error  // the project lookup couldn't tell (a folder it can't search): read by everything that looks in the project, never by the global scope

	homeReal   string           // the resolved ~/.shortcuts, "" when it is not there
	homeErr    error            // why it, or ~, can't be resolved: the project lookup can't tell
	globalReal string           // platform.Realpath(Global), once; "" when the global store is not there or can't be resolved
	stores     map[string]Store // by the dir as the caller passed it: Global, or a realpath'd project root
}

// NewContext names the global store and, if it can, the project root, once. It never fails: a global store it can't
// name is GlobalErr (and then there is no project lookup); a project lookup that can't tell is ProjectErr.
func NewContext(cwd string, cwdErr error) *Context {
	home, set := os.LookupEnv("SHORTCUTS_HOME")
	if !set {
		home = "~/.shortcuts"
	}
	c := &Context{Cwd: cwd, CwdErr: cwdErr, stores: map[string]Store{}}
	global, err := platform.ExpandUser(home)
	switch {
	case err != nil:
		c.GlobalErr = err
	case !strings.HasPrefix(global, "/"):
		c.GlobalErr = fmt.Errorf("SHORTCUTS_HOME must be an absolute path, not '%s'", home)
	default:
		c.Global = global
		c.globalReal, c.GlobalErr = resolveIfThere(c.Global)
	}
	if c.GlobalErr != nil {
		return c
	}
	if userHome, err := platform.ExpandUser("~"); err != nil {
		c.homeErr = err
	} else {
		c.homeReal, c.homeErr = resolveIfThere(platform.Join(userHome, ProjectFile))
	}
	if cwdErr == nil {
		c.Project, c.ProjectErr = c.FindProjectRoot(cwd)
	}
	return c
}

// resolveIfThere is platform.Realpath of p, or "" when nothing is there: a store or project file is optional
// (README "Files": the quick start works with no ~/.shortcuts). Any other error is returned.
func resolveIfThere(p string) (string, error) {
	if there, err := platform.Lexists(p); !there || err != nil {
		return "", err
	}
	return platform.Realpath(p)
}

// IsGlobal reports whether dir is the global store: Global itself, as the callers pass it. A project root is a
// resolved path that is never the global store (neverAProject).
func (c *Context) IsGlobal(dir string) bool { return dir == c.Global }

// neverAProject: ~/.shortcuts is never a project, also when SHORTCUTS_HOME points the global store elsewhere.
// file is the folder's .shortcuts; one that is not there is not the global store.
func (c *Context) neverAProject(file string) (bool, error) {
	real, err := resolveIfThere(file)
	if err != nil || real == "" {
		return false, err
	}
	if real == c.globalReal {
		return true, nil
	}
	return real == c.homeReal, c.homeErr
}

func (c *Context) isProjectRoot(folder string) (bool, error) {
	if never, err := c.neverAProject(platform.Join(folder, ProjectFile)); never || err != nil {
		return false, err
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
	cur, err := platform.Realpath(start)
	if err != nil {
		return "", err
	}
	for {
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
	here, err := platform.Realpath(c.Cwd)
	if err != nil {
		return "", err
	}
	root, err := c.FindProjectRoot(here)
	if err != nil {
		return "", err
	}
	if root != "" && root != here {
		return "", fmt.Errorf("%s is inside the project at %s. Run this from there.", here, root)
	}
	if never, err := c.neverAProject(platform.Join(here, ProjectFile)); err != nil {
		return "", err
	} else if never {
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
	var problems []string
	if isGlobal {
		main = LoadGroup(platform.Join(storeDir, "shortcuts.json"), storeDir, "")
	} else {
		main = LoadProjectGroup(storeDir)
	}
	sources := map[string]string{}
	importsDir := platform.Join(storeDir, "imports")
	if isGlobal {
		entries, err := os.ReadDir(importsDir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) { // a store without imports/ is normal
			problems = append(problems, err.Error())
		}
		for _, e := range entries {
			if filename := e.Name(); strings.HasSuffix(filename, ImportSuffix) {
				sources[strings.TrimSuffix(filename, ImportSuffix)] = platform.Join(importsDir, filename)
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
				expanded, err := platform.ExpandUser(path)
				if err != nil {
					problems = append(problems, fmt.Sprintf("import '%s' in %s: %s", name, main.File, err))
					continue
				}
				sources[name] = filepath.Clean(platform.Join(main.Folder(), expanded))
			}
		}
	}
	var namespaces []NamedGroup
	mainShortcuts := main.Shortcuts()
	for _, name := range slices.Sorted(maps.Keys(sources)) {
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
