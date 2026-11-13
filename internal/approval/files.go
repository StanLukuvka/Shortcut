package approval

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
)

// displayPath returns the path relative to the group's script base, or the path itself when that string
// starts with ".." (a literal string test, so a file named "..x" counts as outside).
func displayPath(g model.Group, path string) string {
	rel := pyos.Relpath(path, g.ScriptBase())
	if strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

// entryFiles returns what entries point at, as {path: content}. A relative cwd adds where it really leads, so
// a symlink moved under it needs approving; the leading ./ keeps that key apart from any script, whose path is
// normalised. origin keeps two entries of the same name apart.
func entryFiles(g model.Group, shortcuts *pyjson.Object, origin string) map[string]string {
	files := map[string]string{}
	for _, name := range shortcuts.Keys {
		data, ok := shortcuts.Vals[name].(*pyjson.Object)
		if !ok {
			continue
		}
		if cwd, ok := data.Vals["cwd"].(string); ok && model.IsRelativeCwd(&cwd) && !strings.Contains(cwd, "\x00") { // a NUL can't run, and the JSON already shows it
			files["./cwd of "+name+origin] = pyos.Realpath(model.ResolveRunDir(&cwd, g, "")) + "\n"
		}
		script, ok := data.Vals["script"].(string)
		if !ok {
			continue
		}
		path, err := model.ResolveScriptPath(g.ScriptBase(), script)
		if err != nil {
			continue
		}
		if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
			continue // a FIFO or device would block or never end; EntryErrors reports it as missing
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue // shows up as a change the moment it becomes readable
		}
		files[displayPath(g, path)] = Reviewable(raw)
	}
	return files
}

// folderFiles is everything in a namespace's scripts folder, as {path: content}: a script it names can run or source
// a file beside it, so a change to any of them needs approving. A symlinked folder in it is recorded as where it leads.
func folderFiles(g model.Group) map[string]string {
	files := map[string]string{}
	if g.Name == "" {
		return files
	}
	pyos.Walk(g.ScriptsDir(), func(dir string, dirs, names []string) {
		real := pyos.Realpath(dir) // as entryFiles resolves a script's path
		for _, name := range dirs {
			link := pyos.Join(dir, name)
			if st, err := os.Lstat(link); err == nil && st.Mode()&os.ModeSymlink != 0 {
				if target, err := os.Readlink(link); err == nil {
					files[displayPath(g, pyos.Join(real, name))] = "symlink to " + target + "\n"
				}
			}
		}
		for _, name := range names {
			path := pyos.Join(real, name)
			if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() { // a FIFO or device would block or never end
				if raw, err := os.ReadFile(path); err == nil {
					files[displayPath(g, path)] = Reviewable(raw)
				}
			}
		}
	})
	return files
}

func jsonFile(g model.Group, path string, data *pyjson.Object) (map[string]string, error) {
	if data == nil {
		data = &pyjson.Object{}
	}
	text, err := pyjson.MarshalIndent(data.Sorted())
	if err != nil {
		return nil, fmt.Errorf("cannot approve: %q: %w", displayPath(g, path), err)
	}
	return map[string]string{displayPath(g, path): string(text) + "\n"}, nil
}

func merge(maps ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range maps {
		for k, v := range m { // later wins, as {**a, **b}
			out[k] = v
		}
	}
	return out
}

// SharedFiles is the group's own file and what it names, the JSON normalised, and for a namespace its scripts
// folder: exactly what is compared, snapshotted and diffed. A project may have no .shortcuts; then this is empty.
func SharedFiles(g model.Group) (map[string]string, error) {
	if g.LocalFile != "" && !pyos.Exists(g.File) {
		return map[string]string{}, nil
	}
	own, err := jsonFile(g, g.File, g.Data)
	if err != nil {
		return nil, err
	}
	return merge(own, folderFiles(g), entryFiles(g, g.SharedShortcuts(), "")), nil
}

// LocalFiles is the same for .shortcuts.local.
func LocalFiles(g model.Group) (map[string]string, error) {
	if g.LocalFile == "" || !pyos.Exists(g.LocalFile) {
		return map[string]string{}, nil
	}
	own, err := jsonFile(g, g.LocalFile, g.LocalData)
	if err != nil {
		return nil, err
	}
	return merge(own, entryFiles(g, g.LocalShortcuts(), " in "+model.LocalFile)), nil
}

// checkPaths refuses a snapshot with text that is not valid UTF-8 in a path or a value: the record is JSON, and
// JSON cannot hold it without merging two different names into one. The smallest offender is named.
func checkPaths(files map[string]string) error {
	for _, p := range slices.Sorted(maps.Keys(files)) {
		if !utf8.ValidString(p) || !utf8.ValidString(files[p]) {
			return fmt.Errorf("cannot approve: %q is not valid UTF-8; rename it", p)
		}
	}
	return nil
}

// snapshot is SharedFiles and LocalFiles, or the error for text that cannot be approved. State, Save and Require
// get the maps this way.
func snapshot(g model.Group) (shared, local map[string]string, err error) {
	if err = CheckKey(g); err != nil {
		return nil, nil, err
	}
	if shared, err = SharedFiles(g); err != nil {
		return nil, nil, err
	}
	if local, err = LocalFiles(g); err != nil {
		return nil, nil, err
	}
	if err = checkPaths(shared); err == nil {
		err = checkPaths(local)
	}
	return shared, local, err
}

// CheckKey refuses a group whose approval key would be built from a path that is not valid UTF-8: the store
// folder, and for a namespace its file. Before any diff or prompt, so nothing is shown for a record that cannot be kept.
func CheckKey(g model.Group) error {
	if !g.NeedsApproval() {
		return nil
	}
	parts := []string{pyos.Realpath(g.StoreDir)}
	if g.Name != "" {
		parts = append(parts, pyos.Realpath(g.File))
	}
	for _, p := range parts {
		if !utf8.ValidString(p) {
			return fmt.Errorf("cannot approve: %q is not valid UTF-8; rename it", p)
		}
	}
	return nil
}

// Check is nil when the group's files can be approved, else the error that says why not.
func Check(g model.Group) error {
	_, _, err := snapshot(g)
	return err
}
