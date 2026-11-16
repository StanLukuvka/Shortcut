package approval

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/store"
)

// displayPath returns the path relative to the group's script base, or the path itself when that string
// starts with ".." (a literal string test, so a file named "..x" counts as outside).
func displayPath(g store.Group, path string) string {
	rel := platform.Relpath(path, g.ScriptBase())
	if strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

// entryFiles returns what entries point at, as {path: sha256}. A relative cwd adds where it really leads, so
// a symlink moved under it needs approving; the leading ./ keeps that key apart from any script, whose path is
// normalised. origin keeps two entries of the same name apart.
func entryFiles(g store.Group, items store.Items, origin string) map[string]string {
	files := map[string]string{}
	for _, it := range items {
		e := it.Entry // set for a broken entry too, with the fields that are well typed
		if e == nil {
			continue
		}
		if store.IsRelativeCwd(e.Cwd) {
			files["./cwd of "+it.Name+origin] = digest([]byte(platform.Realpath(store.ResolveRunDir(e.Cwd, g, "")) + "\n"))
		}
		if e.Script == nil {
			continue
		}
		path, err := store.ResolveScriptPath(g.ScriptBase(), *e.Script)
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
func folderFiles(g store.Group) map[string]string {
	files := map[string]string{}
	if g.Kind != store.KindNamespace {
		return files
	}
	platform.Walk(g.ScriptsDir(), func(dir string, dirs, names []string) {
		real := platform.Realpath(dir) // as entryFiles resolves a script's path
		for _, name := range dirs {
			link := platform.Join(dir, name)
			if st, err := os.Lstat(link); err == nil && st.Mode()&os.ModeSymlink != 0 {
				if target, err := os.Readlink(link); err == nil {
					files[displayPath(g, platform.Join(real, name))] = digest([]byte("symlink to " + target + "\n"))
				}
			}
		}
		for _, name := range names {
			path := platform.Join(real, name)
			if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() { // a FIFO or device would block or never end
				if raw, err := os.ReadFile(path); err == nil {
					files[displayPath(g, path)] = digest(raw)
				}
			}
		}
	})
	return files
}

func jsonFile(g store.Group, path string, d *platform.Doc) (map[string]string, error) {
	text, err := d.Normal()
	if err != nil {
		return nil, fmt.Errorf("cannot approve: %q: %w", displayPath(g, path), err)
	}
	return map[string]string{displayPath(g, path): digest(text)}, nil
}

func merge(maps ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range maps {
		for k, v := range m { // a later map's value wins
			out[k] = v
		}
	}
	return out
}

// SharedFiles is the group's own file and what it names, the JSON normalised, and for a namespace its scripts
// folder: exactly what is compared and snapshotted, each as its SHA-256. A project may have no .shortcuts; then this is empty.
func SharedFiles(g store.Group) (map[string]string, error) {
	if g.Kind == store.KindProject && !platform.Exists(g.File) {
		return map[string]string{}, nil
	}
	own, err := jsonFile(g, g.File, g.Doc)
	if err != nil {
		return nil, err
	}
	return merge(own, folderFiles(g), entryFiles(g, g.Shared, "")), nil
}

// LocalFiles is the same for .shortcuts.local.
func LocalFiles(g store.Group) (map[string]string, error) {
	if g.Kind != store.KindProject || !platform.Exists(g.LocalFile) {
		return map[string]string{}, nil
	}
	own, err := jsonFile(g, g.LocalFile, g.LocalDoc)
	if err != nil {
		return nil, err
	}
	return merge(own, entryFiles(g, g.Local, " in "+store.LocalFile)), nil
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
func snapshot(c *store.Context, g store.Group) (shared, local map[string]string, err error) {
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
func CheckKey(c *store.Context, g store.Group) error {
	if !g.NeedsApproval() {
		return nil
	}
	parts := []string{platform.Realpath(g.StoreDir)}
	if g.Kind == store.KindNamespace {
		parts = append(parts, platform.Realpath(g.File))
	}
	for _, p := range parts {
		if !utf8.ValidString(p) {
			return fmt.Errorf("cannot approve: %q is not valid UTF-8; rename it", p)
		}
	}
	return nil
}

// Check is nil when the group's files can be approved, else the error that says why not.
func Check(c *store.Context, g store.Group) error {
	_, _, err := snapshot(c, g)
	return err
}
