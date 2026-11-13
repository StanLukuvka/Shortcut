package cli

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/approval"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// Adding an entry to a store (writes.py): which file it goes in, under that store's lock, keeping an approval that
// still holds. Lock discipline: the registry lock is never nested and InitStore never runs inside it; for a
// namespace the approval is saved after the lock is released, because Save takes the global store's lock, which
// is the lock a namespace's registry write holds.

func pathExists(path string) bool { _, err := os.Stat(path); return err == nil }

func isDirPath(path string) bool { st, err := os.Stat(path); return err == nil && st.IsDir() }

func isLink(path string) bool {
	st, err := os.Lstat(path)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

// noteIfUnapproved is note_if_unapproved: after a change made through shortcut, say so if it needs approving before
// it runs.
func noteIfUnapproved(s *textio.Streams, g model.Group) {
	if g.NeedsApproval() && approval.State(model.ReloadGroup(g)) != "ok" {
		s.Err.WriteString("note: " + g.Label() + " needs approving before it runs: " + g.UpdateCmd() + "\n")
	}
}

// targetGroup is target_group: the file a new shortcut goes in. It creates nothing; register does.
func targetGroup(ns Namespace, cwd string) (model.Group, error) {
	if err := model.ValidateName(ns.Str("name")); err != nil {
		return model.Group{}, err
	}
	local, namespace := ns.Bool("local"), ns.Str("namespace") // "" is no namespace, as Python's falsy ""
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

// shortcutsOf is reg.setdefault("shortcuts", {}) for a file that was read: a "shortcuts" that is not an object is
// refused where Python's next line would have raised.
func shortcutsOf(path string, reg *pyjson.Object) (*pyjson.Object, error) {
	v, has := reg.Get("shortcuts")
	if !has {
		o := &pyjson.Object{}
		reg.Set("shortcuts", o)
		return o, nil
	}
	o, ok := v.(*pyjson.Object)
	if !ok {
		return nil, fmt.Errorf("corrupt registry %s: 'shortcuts' must be an object", path)
	}
	return o, nil
}

func newRegistry() *pyjson.Object {
	reg := &pyjson.Object{}
	reg.Set("version", big.NewInt(model.RegistryVersion))
	reg.Set("shortcuts", &pyjson.Object{})
	return reg
}

// register is _register: add entry to group's file under the registry lock; writeScript runs first, once the name
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
	var keep map[string]string
	err := model.WithRegistryLock(storeDir, func() error {
		isNewFile := group.Name != "" && !pathExists(group.File)
		approved := namespaceIsApproved(group)
		reg := newRegistry()
		if !isNewFile {
			var err error
			if reg, err = model.LoadRegistryFile(group.File); err != nil {
				return err
			}
		}
		shortcuts, err := shortcutsOf(group.File, reg)
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
		if err := os.MkdirAll(pyos.Dirname(group.File), 0o777); err != nil {
			return err
		}
		if err := model.SaveRegistryFile(group.File, reg); err != nil {
			return err
		}
		if approved {
			keep = approval.SharedFiles(model.ReloadGroup(group))
		}
		return nil
	})
	if err != nil {
		return err
	}
	if keep != nil {
		if err := approval.Save(group, keep, map[string]string{}); err != nil { // after the lock: Save takes the global one
			return err
		}
	}
	noteIfUnapproved(s, group)
	return nil
}

// namespaceIsApproved is namespace_is_approved: before shortcut writes to a global namespace, under its lock,
// whether the file matches its approval. Only then does the approval follow the write, as writeLocal does, so a hand
// edit still needs update.
func namespaceIsApproved(g model.Group) bool {
	return g.Name != "" && pathExists(g.File) && approval.State(model.ReloadGroup(g)) == "ok"
}

// writeLocal is write_local: change .shortcuts.local through change(shortcuts), the only way shortcut writes to a
// project. The approval record follows, but only if .local matched it first: a hand edit, or anything in .shortcuts,
// must not get approved by the way.
func writeLocal(root string, change func(*pyjson.Object) error) (model.Group, error) {
	var after model.Group
	err := model.WithRegistryLock(root, func() error {
		before := model.LoadProjectGroup(root)
		record := approval.Load(before)
		approved := approval.LocalIsApproved(before, record)
		path := before.LocalFile
		isNewFile := !pathExists(path)
		reg := newRegistry()
		if !isNewFile {
			var err error
			if reg, err = model.LoadRegistryFile(path); err != nil {
				return err
			}
		}
		shortcuts, err := shortcutsOf(path, reg)
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
			return approval.Save(after, snapshot, approval.LocalFiles(after))
		}
		return nil
	})
	return after, err
}

// excludeFromGit is exclude_from_git: keep .shortcuts.local out of `git add`, without touching a file the project
// commits. Any failure (no git, not a repository, a file that can't be written) is ignored, as Python's suppress(OSError).
func excludeFromGit(root string) {
	cmd := exec.Command("git", "rev-parse", "--git-path", "info/exclude")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return
	}
	path := pyos.Join(root, strings.TrimSpace(string(out))) // Python's join keeps an absolute result as it is
	if os.MkdirAll(pyos.Dirname(path), 0o777) != nil {
		return
	}
	var text string
	if raw, err := os.ReadFile(path); err == nil {
		text = string(raw)
	} else if pathExists(path) {
		return
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if line == model.LocalFile {
			return
		}
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666)
	if err != nil {
		return
	}
	f.WriteString(model.LocalFile + "\n")
	f.Close()
}

// storedCwd is stored_cwd: --cwd as typed (relative to the user's shell) -> the 'cwd' field (relative to the
// defining file).
func storedCwd(value *string, g model.Group) (*string, error) {
	if value == nil || *value == model.Caller {
		return value, nil
	}
	path := pyos.Realpath(pyos.ExpandUser(*value))
	if !isDirPath(path) {
		return nil, Die("--cwd directory does not exist: " + path)
	}
	if !model.IsGlobalStore(g.StoreDir) && pyos.IsRelativeTo(path, pyos.Realpath(g.StoreDir)) {
		rel := pyos.Relpath(path, pyos.Realpath(g.Folder()))
		return &rel, nil
	}
	return &path, nil
}

// storedScriptField is _stored_script_field: (registry field, absolute path) for a copy kept in the group's scripts
// folder.
func storedScriptField(g model.Group, filename string) (string, string) {
	abs := pyos.Join(g.ScriptsDir(), filename)
	return pyos.Relpath(abs, g.ScriptBase()), abs
}

// refuseSymlinks is refuse_symlinks: a repo can commit scripts/, or one script, as a symlink to a file of yours.
// Your own stores can do what they like.
func refuseSymlinks(g model.Group, path string) error {
	if !g.NeedsApproval() {
		return nil
	}
	base := g.ScriptBase()
	cur := base
	for _, part := range strings.Split(pyos.Relpath(path, base), "/") {
		if part == "" || part == "." {
			continue
		}
		cur = pyos.Join(cur, part)
		if isLink(cur) {
			return errors.New(cur + " is a symlink, so this would write somewhere else")
		}
	}
	return nil
}
