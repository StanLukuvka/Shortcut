package approval

import (
	"os"
	"strings"

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

func jsonFile(g model.Group, path string, data *pyjson.Object) map[string]string {
	if data == nil {
		data = &pyjson.Object{}
	}
	return map[string]string{
		displayPath(g, path): pyjson.Dumps(data, pyjson.Opts{Indent: 2, SortKeys: true}) + "\n",
	}
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
// folder: exactly what is hashed, snapshotted and diffed. A project may have no .shortcuts; then this is empty.
func SharedFiles(g model.Group) map[string]string {
	if g.LocalFile != "" && !pyos.Exists(g.File) {
		return map[string]string{}
	}
	return merge(jsonFile(g, g.File, g.Data), folderFiles(g), entryFiles(g, g.SharedShortcuts(), ""))
}

// LocalFiles is the same for .shortcuts.local.
func LocalFiles(g model.Group) map[string]string {
	if g.LocalFile == "" || !pyos.Exists(g.LocalFile) {
		return map[string]string{}
	}
	return merge(jsonFile(g, g.LocalFile, g.LocalData), entryFiles(g, g.LocalShortcuts(), " in "+model.LocalFile))
}
