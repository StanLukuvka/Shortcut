package approval

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/ordjson"
	"github.com/StanLukuvka/Shortcut/internal/osutil"
)

// displayPath returns the path relative to the group's script base, or the path itself when that string
// starts with ".." (a literal string test, so a file named "..x" counts as outside).
func displayPath(g model.Group, path string) string {
	rel := osutil.Relpath(path, g.ScriptBase())
	if strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

// entryFiles returns what entries point at, as {path: sha256}. A relative cwd adds where it really leads, so
// a symlink moved under it needs approving; the leading ./ keeps that key apart from any script, whose path is
// normalised. origin keeps two entries of the same name apart.
func entryFiles(g model.Group, shortcuts *ordjson.Object, origin string) map[string]string {
	files := map[string]string{}
	for _, name := range shortcuts.Keys {
		data, ok := shortcuts.Vals[name].(*ordjson.Object)
		if !ok {
			continue
		}
		if cwd, ok := data.Vals["cwd"].(string); ok && model.IsRelativeCwd(&cwd) {
			files["./cwd of "+name+origin] = digest([]byte(osutil.Realpath(model.ResolveRunDir(&cwd, g, "")) + "\n"))
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
		files[displayPath(g, path)] = digest(raw)
	}
	return files
}

// folderFiles is everything in a namespace's scripts folder, as {path: sha256}: a script it names can run or source
// a file beside it, so a change to any of them needs approving. A symlinked folder in it is recorded as where it leads.
func folderFiles(g model.Group) map[string]string {
	files := map[string]string{}
	if g.Kind != model.KindNamespace {
		return files
	}
	osutil.Walk(g.ScriptsDir(), func(dir string, dirs, names []string) {
		real := osutil.Realpath(dir) // as entryFiles resolves a script's path
		for _, name := range dirs {
			link := osutil.Join(dir, name)
			if st, err := os.Lstat(link); err == nil && st.Mode()&os.ModeSymlink != 0 {
				if target, err := os.Readlink(link); err == nil {
					files[displayPath(g, osutil.Join(real, name))] = digest([]byte("symlink to " + target + "\n"))
				}
			}
		}
		for _, name := range names {
			path := osutil.Join(real, name)
			if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() { // a FIFO or device would block or never end
				if raw, err := os.ReadFile(path); err == nil {
					files[displayPath(g, path)] = digest(raw)
				}
			}
		}
	})
	return files
}

func jsonFile(g model.Group, path string, data *ordjson.Object) (map[string]string, error) {
	text, err := ordjson.MarshalIndent(data.Sorted())
	if err != nil {
		return nil, fmt.Errorf("cannot approve: %q: %w", displayPath(g, path), err)
	}
	return map[string]string{displayPath(g, path): digest(append(text, '\n'))}, nil
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
// folder: exactly what is compared and snapshotted, each as its SHA-256. A project may have no .shortcuts; then this is empty.
func SharedFiles(g model.Group) (map[string]string, error) {
	if g.Kind == model.KindProject && !osutil.Exists(g.File) {
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
	if g.Kind != model.KindProject || !osutil.Exists(g.LocalFile) {
		return map[string]string{}, nil
	}
	own, err := jsonFile(g, g.LocalFile, g.LocalData)
	if err != nil {
		return nil, err
	}
	return merge(own, entryFiles(g, g.LocalShortcuts(), " in "+model.LocalFile)), nil
}

// checkPaths refuses a snapshot with a path that is not valid UTF-8: the record is JSON, and JSON cannot hold it
// without merging two different names into one. The smallest offender is named. Values are hex digests.
func checkPaths(files map[string]string) error {
	for _, p := range slices.Sorted(maps.Keys(files)) {
		if !utf8.ValidString(p) {
			return fmt.Errorf("cannot approve: %q is not valid UTF-8; rename it", p)
		}
	}
	return nil
}

// snapshot is SharedFiles and LocalFiles, or the error for text that cannot be approved. State, Save and Require
// get the maps this way.
func snapshot(c *model.Context, g model.Group) (shared, local map[string]string, err error) {
	if err = CheckKey(c, g); err != nil {
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
// folder, and for a namespace its file. Before any listing or prompt, so nothing is shown for a record that cannot be kept.
func CheckKey(c *model.Context, g model.Group) error {
	if !g.NeedsApproval() {
		return nil
	}
	parts := []string{osutil.Realpath(g.StoreDir)}
	if g.Kind == model.KindNamespace {
		parts = append(parts, osutil.Realpath(g.File))
	}
	for _, p := range parts {
		if !utf8.ValidString(p) {
			return fmt.Errorf("cannot approve: %q is not valid UTF-8; rename it", p)
		}
	}
	return nil
}

// Check is nil when the group's files can be approved, else the error that says why not.
func Check(c *model.Context, g model.Group) error {
	_, _, err := snapshot(c, g)
	return err
}
