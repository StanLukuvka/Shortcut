// Package write is everything that changes a store: adding an entry and the registry lock.
package write

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/terminal"
)

// LoadForEdit is the registry at path, or a fresh one when isNew, with its "shortcuts" object. A "shortcuts" that is
// not an object is refused.
func LoadForEdit(path string, isNew bool) (reg, shortcuts platform.Doc, err error) {
	if isNew {
		reg = platform.Doc{}
		reg["version"] = []byte(strconv.Itoa(store.RegistryVersion))
		reg["shortcuts"] = []byte("{}")
	} else if reg, err = store.LoadRegistryFile(path); err != nil {
		return nil, nil, err
	}
	if !reg.Has("shortcuts") {
		return reg, platform.Doc{}, nil
	}
	shortcuts, ok := reg.Object("shortcuts")
	if !ok {
		return nil, nil, fmt.Errorf("corrupt registry %s: 'shortcuts' must be an object", path)
	}
	return reg, shortcuts, nil
}

// SaveEdited writes reg with shortcuts, the copy LoadForEdit gave, put back in it.
func SaveEdited(path string, reg, shortcuts platform.Doc) error {
	raw, err := shortcuts.Bytes()
	if err != nil {
		return err
	}
	reg["shortcuts"] = raw
	return store.SaveRegistryFile(path, reg)
}

// Register adds entry to group's file under the registry lock. It creates a missing store or namespace file. A project's group means .shortcuts.local.
func Register(c *store.Context, s *terminal.Streams, group store.Group, entry *store.Entry) error {
	storeDir := group.StoreDir
	if group.Kind == store.KindProject {
		change := func(shortcuts platform.Doc) error {
			if shortcuts.Has(entry.Name) {
				return terminal.Die("shortcut '" + entry.Name + "' already exists in " + group.LocalFile)
			}
			if _, clash := c.ReadStore(storeDir).Namespace(entry.Name); clash {
				return terminal.Die("'" + entry.Name + "' is a namespace in " + storeDir)
			}
			raw, err := entry.Raw()
			if err != nil {
				return err
			}
			shortcuts[entry.Name] = raw
			return nil
		}
		if err := WriteLocal(c, storeDir, change); err != nil {
			return err
		}
		if _, shared := group.Shared.Get(entry.Name); shared {
			s.Err.WriteString("note: this overrides '" + entry.Name + "' from " + store.ProjectFile + " for you\n")
		}
		return nil
	}
	err := c.WithLock(func() error {
		exists, err := platform.Exists(group.File)
		if err != nil {
			return err
		}
		reg, shortcuts, err := LoadForEdit(group.File, !exists)
		if err != nil {
			return err
		}
		if shortcuts.Has(entry.Name) {
			return terminal.Die("shortcut '" + entry.Name + "' already exists in " + group.File)
		}
		if group.Kind != store.KindNamespace {
			if _, clash := c.ReadStore(storeDir).Namespace(entry.Name); clash {
				return terminal.Die("'" + entry.Name + "' is a namespace in store " + storeDir)
			}
		}
		raw, err := entry.Raw()
		if err != nil {
			return err
		}
		shortcuts[entry.Name] = raw
		if err := os.MkdirAll(filepath.Dir(group.File), 0o777); err != nil {
			return err
		}
		return SaveEdited(group.File, reg, shortcuts)
	})
	if err != nil {
		return err
	}
	return nil
}

// WriteLocal changes .shortcuts.local through change(shortcuts), the only way shortcut writes to a project.
func WriteLocal(c *store.Context, root string, change func(platform.Doc) error) error {
	return c.WithLock(func() error {
		path := store.LoadProjectGroup(root).LocalFile
		exists, err := platform.Exists(path)
		if err != nil {
			return err
		}
		reg, shortcuts, err := LoadForEdit(path, !exists)
		if err != nil {
			return err
		}
		if err := change(shortcuts); err != nil {
			return err
		}
		if err := SaveEdited(path, reg, shortcuts); err != nil {
			return err
		}
		return nil
	})
}

// UnlinkFile removes a file or a link, and not one that is already gone; never a folder (os.Remove
// would take an empty one), and a link only ever loses itself.
// The error text is Go's; only a folder where a file should be reaches it.
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
	isLink, err := platform.IsLink(folder)
	if err != nil {
		return err
	}
	if isLink {
		return UnlinkFile(folder)
	}
	isDir, err := platform.IsDir(folder)
	if err != nil || !isDir {
		return err
	}
	return os.RemoveAll(folder) // does not follow a link inside it either
}
