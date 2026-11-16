package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/ordjson"
	"github.com/StanLukuvka/Shortcut/internal/osutil"
)

// Context is what a run knows about where it is: built once per run, passed to every command and to every
// model and approval function that needs the cwd, the global store or the project root. One Context is one run:
// the stores it has read are not read again, except by ReadStore and ReloadGroup.
type Context struct {
	Cwd     string // physical working directory; "" when CwdErr != nil
	CwdErr  error  // read by run and new, which refuse to go on without a cwd
	Global  string // absolute global store dir: $SHORTCUTS_HOME or ~/.shortcuts. The only read of the variable
	Project string // nearest project root at or above Cwd, "" = none

	globalReal string           // osutil.Realpath(Global), once
	stores     map[string]Store // by the dir as the caller passed it: Global, or a realpath'd project root
}

// NewContext resolves the global store and the project root once. It never fails.
func NewContext(cwd string, cwdErr error) *Context {
	home := os.Getenv("SHORTCUTS_HOME")
	if home == "" {
		home = "~/.shortcuts"
	}
	c := &Context{Cwd: cwd, CwdErr: cwdErr, Global: osutil.Abs(osutil.ExpandUser(home)), stores: map[string]Store{}}
	c.globalReal = osutil.Realpath(c.Global)
	if cwdErr == nil {
		c.Project = c.FindProjectRoot(cwd)
	}
	return c
}

// IsGlobal: Path(dir).resolve() == Path(global).resolve().
func (c *Context) IsGlobal(dir string) bool { return osutil.Realpath(dir) == c.globalReal }

// neverAProject: ~/.shortcuts is never a project, also when SHORTCUTS_HOME points the global store elsewhere.
func (c *Context) neverAProject(p string) bool {
	return p == c.globalReal || p == osutil.Realpath(osutil.Join(osutil.ExpandUser("~"), ProjectFile))
}

func (c *Context) isProjectRoot(folder string) bool {
	if c.neverAProject(osutil.Realpath(osutil.Join(folder, ProjectFile))) {
		return false
	}
	return osutil.IsFile(osutil.Join(folder, ProjectFile)) || osutil.IsFile(osutil.Join(folder, LocalFile))
}

// FindProjectRoot returns the nearest folder at or above start with a .shortcuts or .shortcuts.local that is
// not the global store. "" = none.
func (c *Context) FindProjectRoot(start string) string {
	for cur := osutil.Realpath(start); ; {
		if c.isProjectRoot(cur) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// LocalProjectRoot returns where --local writes, the cwd and never a parent. Its errors are
// the messages die prints.
func (c *Context) LocalProjectRoot() (string, error) {
	here := osutil.Realpath(c.Cwd)
	if root := c.FindProjectRoot(here); root != "" && root != here {
		return "", fmt.Errorf("%s is inside the project at %s. Run this from there.", here, root)
	}
	if c.neverAProject(osutil.Realpath(osutil.Join(here, ProjectFile))) {
		return "", fmt.Errorf("%s holds the global store, so it can't hold local shortcuts. Run this inside a project folder.", here)
	}
	if osutil.IsRelativeTo(here, c.globalReal) {
		return "", fmt.Errorf("%s is inside the global store, so it can't hold local shortcuts. Run this inside a project folder.", here)
	}
	if st, err := os.Lstat(osutil.Join(here, LocalFile)); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is a symlink, so writing it would go somewhere else.", osutil.Join(here, LocalFile))
	}
	return here, nil
}

// lockPath is where the registry lock is: <store>/.lock for the global store; otherwise, to keep a repo's root clean,
// <global>/locks/<sha256(realpath(store))>.lock.
func (c *Context) lockPath(storeDir string) string {
	if c.IsGlobal(storeDir) {
		return osutil.Join(storeDir, ".lock")
	}
	sum := sha256.Sum256([]byte(osutil.Realpath(storeDir)))
	return osutil.Join(c.Global, "locks", hex.EncodeToString(sum[:])+".lock")
}

// WithRegistryLock serializes read-modify-write of a store's registry across processes. Not reentrant: never nest
// it, and never call InitStore inside it.
func (c *Context) WithRegistryLock(storeDir string, fn func() error) error {
	return osutil.WithLock(c.lockPath(storeDir), fn)
}

// InitStore creates the global store's empty registry if it has none.
func (c *Context) InitStore(storeDir string) error {
	return c.WithRegistryLock(storeDir, func() error {
		if RegistryExists(storeDir) {
			return nil
		}
		return SaveRegistryAtomic(storeDir, emptyRegistry())
	})
}

// ApprovalPath is <global store>/approvals/<RecordName(key)>.
func (c *Context) ApprovalPath(g Group) string {
	return osutil.Join(c.Global, approvalsDir, RecordName(g.Key()))
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

// Visible are the stores a name is looked up in, nearest first.
func (c *Context) Visible(skipLocal bool) []Store {
	var stores []Store
	if c.Project != "" && !skipLocal {
		stores = append(stores, c.Store(c.Project))
	}
	return append(stores, c.Store(c.Global))
}

// ReloadGroup reads a group's file(s) again, a fresh read each time.
func (c *Context) ReloadGroup(g Group) Group {
	if g.LocalFile != "" {
		return LoadProjectGroup(g.StoreDir)
	}
	return LoadGroup(g.File, g.StoreDir, g.Name)
}

// loadStore reads a store and its namespaces. It never fails on bad content: that is reported on the Group or the
// Store, so list, show and rm still work on a broken store.
// lazy: a folder that can't be listed counts as empty; no test reaches it.
func (c *Context) loadStore(storeDir string) Store {
	isGlobal := c.IsGlobal(storeDir)
	var main Group
	if isGlobal {
		main = LoadGroup(osutil.Join(storeDir, "shortcuts.json"), storeDir, "")
	} else {
		main = LoadProjectGroup(storeDir)
	}
	var problems []string
	sources := map[string]string{}
	importsDir := osutil.Join(storeDir, "imports")
	if isGlobal && osutil.IsDir(importsDir) {
		entries, _ := os.ReadDir(importsDir)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		for _, filename := range slices.Sorted(slices.Values(names)) {
			if strings.HasSuffix(filename, ImportSuffix) {
				sources[strings.TrimSuffix(filename, ImportSuffix)] = osutil.Join(importsDir, filename)
			}
		}
	}
	if imports, ok := main.Data.Vals["imports"].(*ordjson.Object); ok {
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
				sources[name] = filepath.Clean(osutil.Join(main.Folder(), osutil.ExpandUser(path)))
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
