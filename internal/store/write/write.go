// Package write is everything that changes a store: adding an entry, the registry lock, and the approval that follows a write.
package write

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
	"github.com/StanLukuvka/Shortcut/internal/term"
)

// Adding an entry to a store: which file it goes in, under that store's lock, keeping an approval that
// still holds. Lock discipline: the registry lock is never nested and InitStore never runs inside it; for a
// namespace the approval is saved after the lock is released, because Save takes the global store's lock, which
// is the lock a namespace's registry write holds.

// NoteIfUnapproved says so, after a change made through shortcut, if the group needs approving before
// it runs.
func NoteIfUnapproved(c *store.Context, s *term.Streams, g store.Group) {
	if g.NeedsApproval() && approval.Refresh(c, c.ReloadGroup(g)) != store.StateOK {
		s.Err.WriteString("note: " + g.Label() + " needs approving before it runs: " + g.UpdateCmd() + "\n")
	}
}

// LoadForEdit is the registry at path, or a fresh one when isNew, with its "shortcuts" object. A "shortcuts" that is
// not an object is refused.
func LoadForEdit(path string, isNew bool) (reg, shortcuts *platform.Doc, err error) {
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

// SaveEdited writes reg with shortcuts, the copy LoadForEdit gave, put back in it.
func SaveEdited(path string, reg, shortcuts *platform.Doc) error {
	raw, err := shortcuts.Bytes()
	if err != nil {
		return err
	}
	reg.Set("shortcuts", raw)
	return store.SaveRegistryFile(path, reg)
}

// LockedGroupWrite runs mutate under the group's store lock. If the group's approval held before it, the approval
// follows the write, saved after the lock is released: Save takes the global store's lock, which a namespace's
// registry write holds.
func LockedGroupWrite(c *store.Context, group store.Group, mutate func() error) error {
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

// Register adds entry to group's file under the registry lock. It creates a missing store or namespace file. A project's group means .shortcuts.local.
func Register(c *store.Context, s *term.Streams, group store.Group, entry *store.Entry) error {
	storeDir := group.StoreDir
	if group.Kind == store.KindProject {
		change := func(shortcuts *platform.Doc) error {
			if shortcuts.Has(entry.Name) {
				return term.Die("shortcut '" + entry.Name + "' already exists in " + group.LocalFile)
			}
			if _, clash := c.ReadStore(storeDir).Namespace(entry.Name); clash {
				return term.Die("'" + entry.Name + "' is a namespace in " + storeDir)
			}
			raw, err := entry.Raw()
			if err != nil {
				return err
			}
			shortcuts.Set(entry.Name, raw)
			return nil
		}
		after, err := WriteLocal(c, storeDir, change)
		if err != nil {
			return err
		}
		NoteIfUnapproved(c, s, after)
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
	err := LockedGroupWrite(c, group, func() error {
		reg, shortcuts, err := LoadForEdit(group.File, group.Kind == store.KindNamespace && !platform.Exists(group.File))
		if err != nil {
			return err
		}
		if shortcuts.Has(entry.Name) {
			return term.Die("shortcut '" + entry.Name + "' already exists in " + group.File)
		}
		if group.Kind != store.KindNamespace {
			if _, clash := c.ReadStore(storeDir).Namespace(entry.Name); clash {
				return term.Die("'" + entry.Name + "' is a namespace in store " + storeDir)
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
		return SaveEdited(group.File, reg, shortcuts)
	})
	if err != nil {
		return err
	}
	NoteIfUnapproved(c, s, group)
	return nil
}

// namespaceIsApproved reports, before shortcut writes to a global namespace and under its lock,
// whether the file matches its approval. Only then does the approval follow the write, as WriteLocal does, so a hand
// edit still needs update.
func namespaceIsApproved(c *store.Context, g store.Group) bool {
	return g.Kind == store.KindNamespace && platform.Exists(g.File) && approval.Refresh(c, c.ReloadGroup(g)) == store.StateOK
}

// WriteLocal changes .shortcuts.local through change(shortcuts), the only way shortcut writes to a
// project. The approval record follows, but only if .local matched it first: a hand edit, or anything in .shortcuts,
// must not get approved by the way.
func WriteLocal(c *store.Context, root string, change func(*platform.Doc) error) (store.Group, error) {
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
		reg, shortcuts, err := LoadForEdit(path, isNewFile)
		if err != nil {
			return err
		}
		if err := change(shortcuts); err != nil {
			return err
		}
		if err := SaveEdited(path, reg, shortcuts); err != nil {
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

// UnlinkFile removes a file or a link, and not one that is already gone; never a folder (os.Remove
// would take an empty one), and a link only ever loses itself.
// lazy: the error text is Go's; only a folder where a file should be reaches it.
func UnlinkFile(path string) error {
	if err := syscall.Unlink(path); err != nil && !errors.Is(err, syscall.ENOENT) {
		return &os.PathError{Op: "unlink", Path: path, Err: err}
	}
	return nil
}

// RemoveNamespaceFiles removes a namespace's file in imports/ and its scripts folder. A symlinked
// folder loses only the link.
func RemoveNamespaceFiles(group store.Group) error {
	folder := group.ScriptsDir()
	if err := UnlinkFile(group.File); err != nil {
		return err
	}
	switch {
	case platform.IsLink(folder):
		return UnlinkFile(folder)
	case platform.IsDir(folder):
		return os.RemoveAll(folder) // does not follow a link inside it either
	}
	return nil
}
