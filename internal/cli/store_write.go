package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/StanLukuvka/Shortcut/internal/approval"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// Adding an entry to a store: which file it goes in, under that store's lock, keeping an approval that
// still holds. Lock discipline: the registry lock is never nested and InitStore never runs inside it; for a
// namespace the approval is saved after the lock is released, because Save takes the global store's lock, which
// is the lock a namespace's registry write holds.

// noteIfUnapproved says so, after a change made through shortcut, if the group needs approving before
// it runs.
func noteIfUnapproved(s *textio.Streams, g model.Group) {
	if g.NeedsApproval() && approval.State(model.ReloadGroup(g)) != "ok" {
		s.Err.WriteString("note: " + g.Label() + " needs approving before it runs: " + g.UpdateCmd() + "\n")
	}
}

// targetGroup returns the file a new shortcut goes in. It creates nothing; register does.
func targetGroup(ns Namespace, cwd string) (model.Group, error) {
	if err := model.ValidateName(ns.Str("name")); err != nil {
		return model.Group{}, err
	}
	local, namespace := ns.Bool("local"), ns.Str("namespace") // "" is no namespace
	if local && namespace != "" {
		return model.Group{}, Die("--namespace can't be used with --local: " + model.LocalFile + " is one file, and " +
			model.ProjectFile + " is yours to edit")
	}
	storeDir := model.GlobalStoreDir()
	if local {
		var err error
		if storeDir, err = model.LocalProjectRoot(cwd); err != nil {
			return model.Group{}, err
		}
	}
	store := model.LoadStore(storeDir)
	group := store.Main
	if namespace != "" {
		if err := model.ValidateName(namespace); err != nil {
			return model.Group{}, err
		}
		if _, clash := store.Main.Shortcuts().Get(namespace); clash {
			return model.Group{}, Die("'" + namespace + "' is a shortcut in store " + storeDir + ", so it can't be a namespace")
		}
		var ok bool
		if group, ok = store.Namespace(namespace); !ok {
			group = model.Group{Name: namespace, File: pyos.Join(storeDir, "imports", namespace+model.ImportSuffix),
				StoreDir: storeDir, Data: &pyjson.Object{}}
		}
	}
	if group.Error != nil {
		return model.Group{}, Die(*group.Error)
	}
	return group, nil
}

// loadForEdit is the registry at path, or a fresh one when isNew, with its "shortcuts" object. A "shortcuts" that is
// not an object is refused.
func loadForEdit(path string, isNew bool) (*pyjson.Object, *pyjson.Object, error) {
	reg := &pyjson.Object{}
	if isNew {
		reg.Set("version", json.Number(strconv.Itoa(model.RegistryVersion)))
		reg.Set("shortcuts", &pyjson.Object{})
	} else {
		var err error
		if reg, err = model.LoadRegistryFile(path); err != nil {
			return nil, nil, err
		}
	}
	v, has := reg.Get("shortcuts")
	if !has {
		o := &pyjson.Object{}
		reg.Set("shortcuts", o)
		return reg, o, nil
	}
	o, ok := v.(*pyjson.Object)
	if !ok {
		return nil, nil, fmt.Errorf("corrupt registry %s: 'shortcuts' must be an object", path)
	}
	return reg, o, nil
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
func lockedGroupWrite(group model.Group, mutate func() error) error {
	var keep map[string]string
	err := model.WithRegistryLock(group.StoreDir, func() error {
		approved := namespaceIsApproved(group)
		if err := mutate(); err != nil {
			return err
		}
		if approved {
			var err error
			keep, err = approval.SharedFiles(model.ReloadGroup(group))
			return err
		}
		return nil
	})
	if err != nil || keep == nil {
		return err
	}
	return approval.Save(group, keep, map[string]string{})
}

// register adds entry to group's file under the registry lock; writeScript runs first, once the name
// is known free. It creates a missing store or namespace file. A project's group means .shortcuts.local.
func register(s *textio.Streams, group model.Group, entry *model.Entry, writeScript func() error) error {
	storeDir := group.StoreDir
	if group.LocalFile != "" {
		change := func(shortcuts *pyjson.Object) error {
			if v, _ := shortcuts.Get(entry.Name); v != nil { // a null is a hidden shared entry: it is replaced
				return Die("shortcut '" + entry.Name + "' already exists in " + group.LocalFile)
			}
			if _, clash := model.LoadStore(storeDir).Namespace(entry.Name); clash {
				return Die("'" + entry.Name + "' is a namespace in " + storeDir)
			}
			shortcuts.Set(entry.Name, entry.ToObject())
			return nil
		}
		after, err := writeLocal(storeDir, change)
		if err != nil {
			return err
		}
		noteIfUnapproved(s, after)
		if _, shared := group.SharedShortcuts().Get(entry.Name); shared {
			s.Err.WriteString("note: this overrides '" + entry.Name + "' from " + model.ProjectFile + " for you\n")
		}
		return nil
	}
	if !model.RegistryExists(storeDir) {
		if err := model.InitStore(storeDir); err != nil {
			return err
		}
	}
	err := lockedGroupWrite(group, func() error {
		reg, shortcuts, err := loadForEdit(group.File, group.Name != "" && !pyos.Exists(group.File))
		if err != nil {
			return err
		}
		if _, dup := shortcuts.Get(entry.Name); dup {
			return Die("shortcut '" + entry.Name + "' already exists in " + group.File)
		}
		if group.Name == "" {
			if _, clash := model.LoadStore(storeDir).Namespace(entry.Name); clash {
				return Die("'" + entry.Name + "' is a namespace in store " + storeDir)
			}
		}
		if writeScript != nil {
			if err := writeScript(); err != nil {
				return err
			}
		}
		shortcuts.Set(entry.Name, entry.ToObject())
		if err := os.MkdirAll(filepath.Dir(group.File), 0o777); err != nil {
			return err
		}
		return model.SaveRegistryFile(group.File, reg)
	})
	if err != nil {
		return err
	}
	noteIfUnapproved(s, group)
	return nil
}

// namespaceIsApproved reports, before shortcut writes to a global namespace and under its lock,
// whether the file matches its approval. Only then does the approval follow the write, as writeLocal does, so a hand
// edit still needs update.
func namespaceIsApproved(g model.Group) bool {
	return g.Name != "" && pyos.Exists(g.File) && approval.State(model.ReloadGroup(g)) == "ok"
}

// writeLocal changes .shortcuts.local through change(shortcuts), the only way shortcut writes to a
// project. The approval record follows, but only if .local matched it first: a hand edit, or anything in .shortcuts,
// must not get approved by the way.
func writeLocal(root string, change func(*pyjson.Object) error) (model.Group, error) {
	var after model.Group
	err := model.WithRegistryLock(root, func() error {
		before := model.LoadProjectGroup(root)
		if err := approval.CheckKey(before); err != nil { // before anything is written: the approval could not follow
			return err
		}
		record := approval.Load(before)
		approved := approval.LocalIsApproved(before, record)
		path := before.LocalFile
		isNewFile := !pyos.Exists(path)
		reg, shortcuts, err := loadForEdit(path, isNewFile)
		if err != nil {
			return err
		}
		if err := change(shortcuts); err != nil {
			return err
		}
		if err := model.SaveRegistryFile(path, reg); err != nil {
			return err
		}
		if isNewFile {
			excludeFromGit(root)
		}
		after = model.LoadProjectGroup(root)
		if approved {
			snapshot := map[string]string{}
			if record != nil {
				snapshot = record.Snapshot
			}
			local, err := approval.LocalFiles(after)
			if err != nil {
				return err
			}
			return approval.Save(after, snapshot, local)
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
	path := pyos.Join(root, strings.TrimSpace(string(out))) // an absolute result stays as it is
	if os.MkdirAll(filepath.Dir(path), 0o777) != nil {
		return
	}
	var text string
	if raw, err := os.ReadFile(path); err == nil {
		text = string(raw)
	} else if pyos.Exists(path) {
		return
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if line == model.LocalFile {
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
	f.WriteString(sep + model.LocalFile + "\n")
	f.Close()
}

// storedCwd converts --cwd as typed (relative to the user's shell) to the 'cwd' field (relative to the
// defining file).
func storedCwd(value *string, g model.Group) (*string, error) {
	if value == nil || *value == model.Caller {
		return value, nil
	}
	path := pyos.Realpath(pyos.ExpandUser(*value))
	if !pyos.IsDir(path) {
		return nil, Die("--cwd directory does not exist: " + path)
	}
	if !model.IsGlobalStore(g.StoreDir) && pyos.IsRelativeTo(path, pyos.Realpath(g.StoreDir)) {
		rel := pyos.Relpath(path, pyos.Realpath(g.Folder()))
		return &rel, nil
	}
	return &path, nil
}

// storedScriptField returns (registry field, absolute path) for a copy kept in the group's scripts
// folder.
func storedScriptField(g model.Group, filename string) (string, string) {
	abs := pyos.Join(g.ScriptsDir(), filename)
	return pyos.Relpath(abs, g.ScriptBase()), abs
}

// refuseSymlinks refuses symlinks: a repo can commit scripts/, or one script, as a symlink to a file of yours.
// Your own stores can do what they like.
func refuseSymlinks(g model.Group, path string) error {
	if !g.NeedsApproval() {
		return nil
	}
	if link := firstLinkUnder(g.ScriptBase(), pyos.Relpath(path, g.ScriptBase())); link != "" {
		return errors.New(link + " is a symlink, so this would write somewhere else")
	}
	return nil
}

// firstLinkUnder is the first symlink on the way from base down rel, "" if none.
func firstLinkUnder(base, rel string) string {
	cur := base
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." {
			continue
		}
		cur = pyos.Join(cur, part)
		if pyos.IsLink(cur) {
			return cur
		}
	}
	return ""
}

// prepareScriptDest refuses a script path that is, or sits under, a link or an existing file, and makes the folder.
func prepareScriptDest(group model.Group, abs string) error {
	if err := refuseSymlinks(group, abs); err != nil {
		return err
	}
	if err := refuseExisting(abs); err != nil {
		return err
	}
	return os.MkdirAll(group.ScriptsDir(), 0o777)
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
func removeNamespaceFiles(group model.Group) error {
	folder := group.ScriptsDir()
	if err := unlinkFile(group.File); err != nil {
		return err
	}
	switch {
	case pyos.IsLink(folder):
		return unlinkFile(folder)
	case pyos.IsDir(folder):
		return os.RemoveAll(folder) // does not follow a link inside it either
	}
	return nil
}

// isListedImport reports whether a namespace comes from the store file's "imports" rather than its
// imports/ folder.
func isListedImport(store model.Store, name string) bool {
	if store.Main.Data == nil {
		return false
	}
	imports, ok := store.Main.Data.Vals["imports"].(*pyjson.Object)
	if !ok {
		return false
	}
	_, has := imports.Get(name)
	return has
}
