package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/store/approval"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// Adding an entry to a store: which file it goes in, under that store's lock, keeping an approval that
// still holds. Lock discipline: the registry lock is never nested and InitStore never runs inside it; for a
// namespace the approval is saved after the lock is released, because Save takes the global store's lock, which
// is the lock a namespace's registry write holds.

// noteIfUnapproved says so, after a change made through shortcut, if the group needs approving before
// it runs.
func noteIfUnapproved(c *store.Context, s *textio.Streams, g store.Group) {
	if g.NeedsApproval() && approval.Refresh(c, c.ReloadGroup(g)) != store.StateOK {
		s.Err.WriteString("note: " + g.Label() + " needs approving before it runs: " + g.UpdateCmd() + "\n")
	}
}

// targetGroup returns the file a new shortcut goes in. It creates nothing; register does.
func targetGroup(c *store.Context, ns Namespace) (store.Group, error) {
	if err := store.ValidateName(ns.Str("name")); err != nil {
		return store.Group{}, err
	}
	local, namespace := ns.Bool("local"), ns.Str("namespace")
	if local && namespace != "" {
		return store.Group{}, Die("--namespace can't be used with --local: " + store.LocalFile + " is one file, and " +
			store.ProjectFile + " is yours to edit")
	}
	storeDir := c.Global
	if local {
		var err error
		if storeDir, err = c.LocalProjectRoot(); err != nil {
			return store.Group{}, err
		}
	}
	st := c.Store(storeDir)
	group := st.Main
	if namespace != "" {
		if err := store.ValidateName(namespace); err != nil {
			return store.Group{}, err
		}
		if _, clash := st.Main.Shortcuts().Get(namespace); clash {
			return store.Group{}, Die("'" + namespace + "' is a shortcut in store " + storeDir + ", so it can't be a namespace")
		}
		var ok bool
		if group, ok = st.Namespace(namespace); !ok {
			group = store.Group{Kind: store.KindNamespace, InProject: !c.IsGlobal(storeDir), Name: namespace, File: platform.Join(storeDir, "imports", namespace+store.ImportSuffix),
				StoreDir: storeDir, Doc: &platform.Doc{}}
		}
	}
	if group.Error != nil {
		return store.Group{}, Die(*group.Error)
	}
	return group, nil
}

// loadForEdit is the registry at path, or a fresh one when isNew, with its "shortcuts" object. A "shortcuts" that is
// not an object is refused.
func loadForEdit(path string, isNew bool) (reg, shortcuts *platform.Doc, err error) {
	if isNew {
		reg = &platform.Doc{}
		reg.Set("version", []byte(strconv.Itoa(store.RegistryVersion)))
		reg.Set("shortcuts", []byte("{}"))
	} else if reg, err = store.LoadRegistryFile(path); err != nil {
		return nil, nil, err
	}
	if !reg.Has("shortcuts") {
		return reg, &platform.Doc{}, nil
	}
	shortcuts, ok := reg.Object("shortcuts")
	if !ok {
		return nil, nil, fmt.Errorf("corrupt registry %s: 'shortcuts' must be an object", path)
	}
	return reg, shortcuts, nil
}

// saveEdited writes reg with shortcuts, the copy loadForEdit gave, put back in it.
func saveEdited(path string, reg, shortcuts *platform.Doc) error {
	raw, err := shortcuts.Bytes()
	if err != nil {
		return err
	}
	reg.Set("shortcuts", raw)
	return store.SaveRegistryFile(path, reg)
}

// confirmed asks c and turns a "no" into exit 1; nil means go ahead.
func confirmed(s *textio.Streams, c textio.Confirm, assumeYes bool) error {
	granted, err := c.Granted(s, assumeYes)
	if err != nil {
		return err
	}
	if !granted {
		return &ExitError{Code: 1}
	}
	return nil
}

// lockedGroupWrite runs mutate under the group's store lock. If the group's approval held before it, the approval
// follows the write, saved after the lock is released: Save takes the global store's lock, which a namespace's
// registry write holds.
func lockedGroupWrite(c *store.Context, group store.Group, mutate func() error) error {
	var keep map[string]string
	err := c.WithRegistryLock(group.StoreDir, func() error {
		approved := namespaceIsApproved(c, group)
		if err := mutate(); err != nil {
			return err
		}
		if approved {
			var err error
			keep, err = approval.SharedFiles(c.ReloadGroup(group))
			return err
		}
		return nil
	})
	if err != nil || keep == nil {
		return err
	}
	return approval.Save(c, group, keep, map[string]string{})
}

// register adds entry to group's file under the registry lock. It creates a missing store or namespace file. A project's group means .shortcuts.local.
func register(c *store.Context, s *textio.Streams, group store.Group, entry *store.Entry) error {
	storeDir := group.StoreDir
	if group.Kind == store.KindProject {
		change := func(shortcuts *platform.Doc) error {
			if shortcuts.Has(entry.Name) {
				return Die("shortcut '" + entry.Name + "' already exists in " + group.LocalFile)
			}
			if _, clash := c.ReadStore(storeDir).Namespace(entry.Name); clash {
				return Die("'" + entry.Name + "' is a namespace in " + storeDir)
			}
			raw, err := entry.Raw()
			if err != nil {
				return err
			}
			shortcuts.Set(entry.Name, raw)
			return nil
		}
		after, err := writeLocal(c, storeDir, change)
		if err != nil {
			return err
		}
		noteIfUnapproved(c, s, after)
		if _, shared := group.Shared.Get(entry.Name); shared {
			s.Err.WriteString("note: this overrides '" + entry.Name + "' from " + store.ProjectFile + " for you\n")
		}
		return nil
	}
	if !store.RegistryExists(storeDir) {
		if err := c.InitStore(storeDir); err != nil {
			return err
		}
	}
	err := lockedGroupWrite(c, group, func() error {
		reg, shortcuts, err := loadForEdit(group.File, group.Kind == store.KindNamespace && !platform.Exists(group.File))
		if err != nil {
			return err
		}
		if shortcuts.Has(entry.Name) {
			return Die("shortcut '" + entry.Name + "' already exists in " + group.File)
		}
		if group.Kind != store.KindNamespace {
			if _, clash := c.ReadStore(storeDir).Namespace(entry.Name); clash {
				return Die("'" + entry.Name + "' is a namespace in store " + storeDir)
			}
		}
		raw, err := entry.Raw()
		if err != nil {
			return err
		}
		shortcuts.Set(entry.Name, raw)
		if err := os.MkdirAll(filepath.Dir(group.File), 0o777); err != nil {
			return err
		}
		return saveEdited(group.File, reg, shortcuts)
	})
	if err != nil {
		return err
	}
	noteIfUnapproved(c, s, group)
	return nil
}

// namespaceIsApproved reports, before shortcut writes to a global namespace and under its lock,
// whether the file matches its approval. Only then does the approval follow the write, as writeLocal does, so a hand
// edit still needs update.
func namespaceIsApproved(c *store.Context, g store.Group) bool {
	return g.Kind == store.KindNamespace && platform.Exists(g.File) && approval.Refresh(c, c.ReloadGroup(g)) == store.StateOK
}

// writeLocal changes .shortcuts.local through change(shortcuts), the only way shortcut writes to a
// project. The approval record follows, but only if .local matched it first: a hand edit, or anything in .shortcuts,
// must not get approved by the way.
func writeLocal(c *store.Context, root string, change func(*platform.Doc) error) (store.Group, error) {
	var after store.Group
	err := c.WithRegistryLock(root, func() error {
		before := store.LoadProjectGroup(root)
		if err := approval.CheckKey(c, before); err != nil { // before anything is written: the approval could not follow
			return err
		}
		record := approval.Load(c, before)
		approved := approval.LocalIsApproved(c, before, record)
		path := before.LocalFile
		isNewFile := !platform.Exists(path)
		reg, shortcuts, err := loadForEdit(path, isNewFile)
		if err != nil {
			return err
		}
		if err := change(shortcuts); err != nil {
			return err
		}
		if err := saveEdited(path, reg, shortcuts); err != nil {
			return err
		}
		if isNewFile {
			excludeFromGit(root)
		}
		after = store.LoadProjectGroup(root)
		if approved {
			snapshot := map[string]string{}
			if record != nil {
				snapshot = record.Snapshot
			}
			local, err := approval.LocalFiles(after)
			if err != nil {
				return err
			}
			return approval.Save(c, after, snapshot, local)
		}
		return nil
	})
	return after, err
}

// excludeFromGit keeps .shortcuts.local out of `git add`, without touching a file the project
// commits. Any failure (no git, not a repository, a file that can't be written) is ignored.
func excludeFromGit(root string) {
	cmd := exec.Command("git", "rev-parse", "--git-path", "info/exclude")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return
	}
	path := platform.Join(root, strings.TrimSpace(string(out))) // an absolute result stays as it is
	if os.MkdirAll(filepath.Dir(path), 0o777) != nil {
		return
	}
	var text string
	if raw, err := os.ReadFile(path); err == nil {
		text = string(raw)
	} else if platform.Exists(path) {
		return
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if line == store.LocalFile {
			return
		}
	}
	sep := ""
	if text != "" && !strings.HasSuffix(text, "\n") {
		sep = "\n"
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666)
	if err != nil {
		return
	}
	f.WriteString(sep + store.LocalFile + "\n")
	f.Close()
}

// storedCwd converts --cwd as typed (relative to the user's shell) to the 'cwd' field (relative to the
// defining file).
func storedCwd(c *store.Context, value *string, g store.Group) (*string, error) {
	if value == nil || *value == store.Caller {
		return value, nil
	}
	path := platform.Realpath(platform.ExpandUser(*value))
	if !platform.IsDir(path) {
		return nil, Die("--cwd directory does not exist: " + path)
	}
	if g.InProject && platform.IsRelativeTo(path, platform.Realpath(g.StoreDir)) {
		rel := platform.Relpath(path, platform.Realpath(g.Folder()))
		return &rel, nil
	}
	return &path, nil
}

// unlinkFile removes a file or a link, and not one that is already gone; never a folder (os.Remove
// would take an empty one), and a link only ever loses itself.
// lazy: the error text is Go's; only a folder where a file should be reaches it.
func unlinkFile(path string) error {
	if err := syscall.Unlink(path); err != nil && !errors.Is(err, syscall.ENOENT) {
		return &os.PathError{Op: "unlink", Path: path, Err: err}
	}
	return nil
}

// removeNamespaceFiles removes a namespace's file in imports/ and its scripts folder. A symlinked
// folder loses only the link.
func removeNamespaceFiles(group store.Group) error {
	folder := group.ScriptsDir()
	if err := unlinkFile(group.File); err != nil {
		return err
	}
	switch {
	case platform.IsLink(folder):
		return unlinkFile(folder)
	case platform.IsDir(folder):
		return os.RemoveAll(folder) // does not follow a link inside it either
	}
	return nil
}

// isListedImport reports whether a namespace comes from the store file's "imports" rather than its
// imports/ folder.
func isListedImport(st store.Store, name string) bool {
	imports, ok := st.Main.Doc.Object("imports")
	return ok && imports.Has(name)
}
