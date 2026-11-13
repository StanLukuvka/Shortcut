package cli

import (
	"errors"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/StanLukuvka/Shortcut/internal/approval"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// What init --global copies into the global store (importing.py, helpers.py): a project's exported shortcuts, or a
// folder of namespace files. Everything is planned from bytes read once, shown, and only then written, so what is
// copied is what was reviewed. Declining writes nothing: no store, no folder, no script.

// storedScript is one script copy a project import will make: where it goes and what it holds.
type storedScript struct {
	path string
	raw  []byte
}

// nsFile is one file of a namespace plan: its path under imports/, its bytes, and whether it was executable.
type nsFile struct {
	rel  string
	raw  []byte
	exec bool
}

// nsPlan is one namespace to copy whole: <ns>.shortcut first, then its scripts/<ns>/ folder.
type nsPlan struct {
	name  string
	files []nsFile
}

// importFromRoot is the part of `init --global SOURCE` that follows once the source is a folder on disk: a project
// (.shortcuts at its root) brings its exported entries; a folder of <ns>.shortcut files brings its namespaces. label
// is what the review says the files came from (the source as typed; P-8 passes "the bundled shortcuts"). A "no" is
// exit 1, with nothing written.
func importFromRoot(s *textio.Streams, label, sourceRoot, destStoreDir string, assumeYes, replace bool) error {
	sourceFile := pyos.Join(sourceRoot, model.ProjectFile)
	names, err := listDir(sourceRoot)
	if err != nil {
		return err
	}
	if !isFilePath(sourceFile) {
		for _, name := range names {
			if strings.HasSuffix(name, model.ImportSuffix) {
				return initNamespaces(s, label, sourceRoot, destStoreDir, assumeYes, replace)
			}
		}
		return Die("no " + model.ProjectFile + " or <ns>" + model.ImportSuffix + " found in " + sourceRoot)
	}
	if replace {
		return Die("--replace is for a folder of namespaces, and " + sourceRoot + " has a " + model.ProjectFile)
	}

	sourceRegistry, err := model.LoadRegistryFile(sourceFile)
	if err != nil {
		return err
	}
	dest := &pyjson.Object{}
	if model.RegistryExists(destStoreDir) {
		if dest, err = model.LoadRegistry(destStoreDir); err != nil {
			return err
		}
	}
	entries, scripts, skipped, err := planImport(sourceRoot, destStoreDir, dest, sourceRegistry)
	if err != nil {
		return err
	}
	// A source with a .shortcuts brings its exported entries alone
	for _, name := range names {
		if strings.HasSuffix(name, model.ImportSuffix) && name != model.ImportSuffix {
			skipped = append(skipped, name+" (a namespace file: a source with a "+model.ProjectFile+" brings only its exported shortcuts)")
		}
	}
	if listed, ok := sourceRegistry.Vals["imports"].(*pyjson.Object); ok && len(listed.Keys) > 0 {
		keys := slices.SortedFunc(slices.Values(listed.Keys), pyjsonCmp)
		skipped = append(skipped, "imports: "+strings.Join(keys, ", ")+" (namespaces a "+model.ProjectFile+" imports aren't copied)")
	}
	printSkipped(s, skipped)
	if len(entries.Keys) == 0 {
		return nil
	}

	// Global shortcuts have no approval step, so an import is reviewed here or never
	files := map[string]string{"shortcuts.json": pyjson.Dumps(entries, pyjson.Opts{Indent: 2, SortKeys: true, EnsureASCII: true}) + "\n"}
	for _, sc := range scripts {
		files[pyos.Relpath(sc.path, destStoreDir)] = approval.Reviewable(sc.raw)
	}
	s.Out.WriteString("New from " + label + ":\n")
	textio.ShowDiff(s, approval.DiffFiles(map[string]string{}, files))
	if err := review(s, label, map[string]string{}, files, assumeYes); err != nil {
		return err
	}
	if err := applyImport(destStoreDir, entries, scripts); err != nil {
		return err
	}
	s.Out.WriteString("Imported (" + strconv.Itoa(len(entries.Keys)) + "):\n")
	for _, name := range entries.Keys {
		s.Out.WriteString("  " + name + "\n")
	}
	return nil
}

// reviewing is set while a question is on the terminal: Ctrl-C there is Ask's "no", not an exit (see cmdInit).
var reviewing atomic.Bool

// review is confirm_review(...).granted(): nil when the import may go ahead, else the "no" (exit 1) or the reason
// there was nobody to ask.
func review(s *textio.Streams, label string, old, files map[string]string, assumeYes bool) error {
	reviewing.Store(true)
	granted, err := approval.ConfirmReview("import", old, files, "import "+label+" in a terminal, or pass -y").Granted(s, assumeYes)
	if err != nil || !granted {
		// reviewing stays set: a Ctrl-C that ended the question reaches cloneTemp's handler after this returns, and
		// must still read as the question's "no" (the command is ending anyway)
		if err != nil {
			return err
		}
		return &ExitError{Code: 1}
	}
	reviewing.Store(false)
	return nil
}

func printSkipped(s *textio.Streams, skipped []string) {
	if len(skipped) == 0 {
		return
	}
	s.Out.WriteString("Skipped (" + strconv.Itoa(len(skipped)) + "):\n")
	for _, line := range skipped {
		s.Out.WriteString("  " + line + "\n")
	}
}

func pyjsonCmp(a, b string) int {
	switch {
	case pyjson.Less(a, b):
		return -1
	case pyjson.Less(b, a):
		return 1
	}
	return 0
}

// listDir is sorted(os.listdir(dir)).
func listDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	slices.SortFunc(names, pyjsonCmp)
	return names, nil
}

func isFilePath(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// pySuffix is pathlib.PurePath(p).suffix: "" for no name or no dot, and a leading or trailing dot is no suffix.
func pySuffix(p string) string {
	name := ""
	for _, part := range strings.Split(p, "/") {
		if part != "" && part != "." {
			name = part
		}
	}
	if i := strings.LastIndex(name, "."); i > 0 && i < len(name)-1 {
		return name[i:]
	}
	return ""
}

// planImport is plan_import: what a source's .shortcuts would add to the global store, changing nothing: the
// entries, the script copies (bytes read once here), and what was skipped and why. Only exported entries come.
func planImport(sourceBase, destStoreDir string, destRegistry, sourceRegistry *pyjson.Object) (*pyjson.Object, []storedScript, []string, error) {
	entries := &pyjson.Object{}
	var scripts []storedScript
	var skipped []string
	destShortcuts, _ := destRegistry.Vals["shortcuts"].(*pyjson.Object)
	if destShortcuts == nil {
		destShortcuts = &pyjson.Object{}
	}
	dest := model.LoadStore(destStoreDir)
	sourceShortcuts, ok := sourceRegistry.Vals["shortcuts"].(*pyjson.Object)
	if !ok {
		return entries, scripts, skipped, nil
	}

	for _, name := range sourceShortcuts.Keys {
		data := sourceShortcuts.Vals[name]
		entry, err := model.EntryFromValue(name, data)
		if err == nil {
			if msg := model.PlacementError(entry, true, false); msg != "" { // 'from' is promote's, never a source's
				err = errors.New(msg)
			}
		}
		if err != nil {
			skipped = append(skipped, name+" (invalid entry: "+err.Error()+")")
			continue
		}
		entryData := data.(*pyjson.Object)
		if !entry.Export {
			skipped = append(skipped, name+" (project-only)")
			continue
		}
		if _, has := destShortcuts.Get(name); has {
			skipped = append(skipped, name+" (already exists)")
			continue
		}
		if _, has := dest.Namespace(name); has {
			skipped = append(skipped, name+" (already a namespace)")
			continue
		}
		scriptField, _ := entryData.Vals["script"].(string)
		if scriptField != "" && strings.HasPrefix(scriptField, "/") {
			skipped = append(skipped, name+" (linked external script)")
			continue
		}
		if err := model.ValidateName(name); err != nil { // untrusted source
			skipped = append(skipped, pyRepr(name)+" (invalid name: "+err.Error()+")")
			continue
		}

		// A relative cwd means nothing once the entry leaves the file that defined it
		if entry.Cwd != nil && model.IsRelativeCwd(entry.Cwd) {
			skipped = append(skipped, name+" (exported, but its cwd '"+*entry.Cwd+"' only exists in the project)")
			continue
		}
		if d := entry.Cwd; d != nil && *d != "" && *d != model.Caller && !isDirPath(pyos.ExpandUser(*d)) {
			skipped = append(skipped, name+" (cwd not found here: "+*d+")")
			continue
		}

		newEntry := &pyjson.Object{}
		for _, k := range entryData.Keys {
			if k != "export" { // a global entry can't export
				newEntry.Set(k, entryData.Vals[k])
			}
		}
		if scriptField != "" {
			cur, linked := sourceBase, false
			for _, part := range strings.Split(scriptField, "/") {
				if part == "" || part == "." {
					continue
				}
				cur = pyos.Join(cur, part)
				if isLink(cur) {
					linked = true
					break
				}
			}
			if linked {
				skipped = append(skipped, name+" (symlink in source)")
				continue
			}
			srcPath, err := model.ResolveScriptPath(sourceBase, scriptField)
			if err != nil {
				skipped = append(skipped, name+" (script path escapes the source: "+scriptField+")")
				continue
			}
			if !isFilePath(srcPath) {
				skipped = append(skipped, name+" (missing source script: "+scriptField+")")
				continue
			}
			destRel, destAbs := storedScriptField(dest.Main, name+orSh(pySuffix(scriptField)))
			if _, err := os.Lstat(destAbs); err == nil || slices.ContainsFunc(scripts, func(sc storedScript) bool { return sc.path == destAbs }) {
				skipped = append(skipped, name+" (would replace "+destRel+")")
				continue
			}
			raw, err := os.ReadFile(srcPath)
			if err != nil {
				return nil, nil, nil, err
			}
			scripts = append(scripts, storedScript{destAbs, raw})
			newEntry.Set("script", destRel)
		}
		entries.Set(name, newEntry)
	}
	return entries, scripts, skipped, nil
}

func orSh(suffix string) string {
	if suffix == "" {
		return ".sh"
	}
	return suffix
}

// planNamespaces is plan_namespaces: what a folder of namespace files would add to the global store's imports/,
// changing nothing. A namespace comes whole, as <ns>.shortcut and its scripts/<ns>/ folder, the layout it has in
// imports/, or not at all. With replace, one already in imports/ is planned too, to go in place of what's there.
func planNamespaces(sourceRoot, destStoreDir string, replace bool) ([]nsPlan, []string, error) {
	var plans []nsPlan
	var skipped []string
	dest := model.LoadStore(destStoreDir)
	importsDir := pyos.Join(destStoreDir, "imports")
	names, err := listDir(sourceRoot)
	if err != nil {
		return nil, nil, err
	}
	for _, filename := range names {
		if !strings.HasSuffix(filename, model.ImportSuffix) {
			continue
		}
		ns := strings.TrimSuffix(filename, model.ImportSuffix)
		path := pyos.Join(sourceRoot, filename)
		folder := pyos.Join("scripts", ns)
		if err := model.ValidateName(ns); err != nil { // untrusted source
			skipped = append(skipped, filename+" ("+err.Error()+")")
			continue
		}
		installed, isInstalled := dest.Namespace(ns)
		listed := isListedImport(dest, ns)
		replacing := replace && isInstalled && !listed
		_, isShortcut := dest.Main.Shortcuts().Get(ns)
		switch {
		case isLink(path) || !isFilePath(path):
			skipped = append(skipped, ns+" (not a regular file: "+filename+")")
			continue
		case replace && listed:
			skipped = append(skipped, ns+" (imported from "+installed.File+", so it isn't replaced)")
			continue
		case !replacing && (isInstalled || lexists(pyos.Join(importsDir, filename))):
			skipped = append(skipped, ns+" (already a namespace; --replace puts this one in its place)")
			continue
		case isShortcut:
			skipped = append(skipped, ns+" (already a shortcut)")
			continue
		case !replacing && lexists(pyos.Join(importsDir, folder)):
			skipped = append(skipped, ns+" (would replace imports/"+folder+")")
			continue
		}
		group := model.LoadGroup(path, sourceRoot, ns)
		if group.Error != nil {
			skipped = append(skipped, ns+" ("+*group.Error+")")
			continue
		}
		if problem := namespaceFolderProblem(group, sourceRoot, folder); problem != "" {
			skipped = append(skipped, ns+" ("+problem+")")
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		plan := nsPlan{name: ns, files: []nsFile{{filename, raw, false}}}
		var walkErr error
		pyos.Walk(pyos.Join(sourceRoot, folder), func(dir string, _, files []string) {
			for _, name := range files {
				file := pyos.Join(dir, name)
				raw, err := os.ReadFile(file)
				if err != nil && walkErr == nil {
					walkErr = err
				}
				plan.files = append(plan.files, nsFile{pyos.Relpath(file, sourceRoot), raw, syscall.Access(file, 1) == nil})
			}
		})
		if walkErr != nil {
			return nil, nil, walkErr
		}
		plans = append(plans, plan)
	}
	return plans, skipped, nil
}

func lexists(path string) bool { _, err := os.Lstat(path); return err == nil }

// namespaceFolderProblem is namespace_folder_problem: why a namespace in a source can't be copied as it is: a script
// outside its scripts folder or missing, or a symlink, which would copy something else. "" is none.
func namespaceFolderProblem(group model.Group, sourceRoot, folder string) string {
	shared := group.SharedShortcuts()
	for _, name := range shared.Keys {
		data, ok := shared.Vals[name].(*pyjson.Object)
		if !ok {
			continue
		}
		script, ok := data.Vals["script"].(string)
		if !ok {
			continue
		}
		if !strings.HasPrefix(pyos.Normpath(script), folder+"/") {
			return "'" + name + "' has its script outside " + folder + "/: " + script
		}
		if !isFilePath(pyos.Join(sourceRoot, script)) {
			return "'" + name + "' has a missing script: " + script
		}
	}
	parts := []string{pyos.Join(sourceRoot, "scripts"), pyos.Join(sourceRoot, folder)}
	pyos.Walk(pyos.Join(sourceRoot, folder), func(dir string, dirs, names []string) {
		for _, n := range slices.Concat(dirs, names) {
			parts = append(parts, pyos.Join(dir, n))
		}
	})
	for _, p := range parts {
		if isLink(p) {
			return "symlink in source: " + pyos.Relpath(p, sourceRoot)
		}
	}
	return ""
}

// installedFiles is installed_files: a namespace in imports/ as init shows it, its file and its scripts folder, under
// imports/.
func installedFiles(group model.Group, importsDir string) map[string]string {
	paths := []string{group.File}
	pyos.Walk(group.ScriptsDir(), func(dir string, _, names []string) {
		for _, n := range names {
			paths = append(paths, pyos.Join(dir, n))
		}
	})
	files := map[string]string{}
	for _, path := range paths {
		if !isFilePath(path) {
			continue
		}
		if raw, err := os.ReadFile(path); err == nil { // Python's suppress(OSError)
			files[pyos.Join("imports", pyos.Relpath(path, importsDir))] = approval.Reviewable(raw)
		}
	}
	return files
}

// initNamespaces is init_namespaces: init --global from a folder of <ns>.shortcut files: copy each into imports/, or
// with replace in place of the one there. Like any namespace, it runs once shortcut update approves it.
func initNamespaces(s *textio.Streams, label, sourceRoot, destStoreDir string, assumeYes, replace bool) error {
	plans, skipped, err := planNamespaces(sourceRoot, destStoreDir, replace)
	if err != nil {
		return err
	}
	installed := model.LoadStore(destStoreDir)
	importsDir := pyos.Join(destStoreDir, "imports")
	old := map[string]string{}
	var keep []nsPlan
	for _, plan := range plans {
		group, isInstalled := installed.Namespace(plan.name)
		if !isInstalled {
			keep = append(keep, plan)
			continue
		}
		before := installedFiles(group, importsDir)
		if maps.Equal(before, planFiles(plan)) {
			skipped = append(skipped, plan.name+" (the same as what's installed)")
		} else {
			maps.Copy(old, before)
			keep = append(keep, plan)
		}
	}
	plans = keep
	printSkipped(s, skipped)
	if len(plans) == 0 {
		return nil
	}
	files := map[string]string{}
	for _, plan := range plans {
		maps.Copy(files, planFiles(plan))
	}
	head := "New"
	if len(old) > 0 {
		head = "Changes"
	}
	s.Out.WriteString(head + " from " + label + ":\n")
	textio.ShowDiff(s, approval.DiffFiles(old, files))
	if err := review(s, label, old, files, assumeYes); err != nil {
		return err
	}
	var replaced []model.Group
	for _, plan := range plans {
		if g, ok := installed.Namespace(plan.name); ok {
			replaced = append(replaced, g)
		}
	}
	if err := applyNamespaces(destStoreDir, plans, replaced); err != nil {
		return err
	}
	s.Out.WriteString("Imported (" + strconv.Itoa(len(plans)) + "), to approve before they run:\n")
	for _, plan := range plans {
		s.Out.WriteString("  shortcut update " + plan.name + "\n")
	}
	return nil
}

// planFiles is {imports/<rel>: reviewable(raw)} for a plan.
func planFiles(plan nsPlan) map[string]string {
	m := map[string]string{}
	for _, f := range plan.files {
		m[pyos.Join("imports", f.rel)] = approval.Reviewable(f.raw)
	}
	return m
}

// writeNew creates path, and its folders, holding raw, and never over a file that is already there (O_EXCL, which
// does not follow a link at the end either). Executable adds the execute bits the file got from its umask.
// lazy: an existing file is reported with Go's text ("file exists"), not Python's "[Errno 17] ...".
func writeNew(path string, raw []byte, executable bool) error {
	if err := os.MkdirAll(pyos.Dirname(path), 0o777); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if executable {
		st, err := os.Stat(path)
		if err != nil {
			return err
		}
		return os.Chmod(path, st.Mode().Perm()|0o111)
	}
	return nil
}

// applyImport is apply_import: the script copies (executable), then the entries, in the registry under its lock.
// Python opens a script with "wb"; this refuses to write over one that appeared since the review, which is what the
// plan's "would replace" check is for.
func applyImport(storeDir string, entries *pyjson.Object, scripts []storedScript) error {
	if err := model.InitStore(storeDir); err != nil {
		return err
	}
	for _, sc := range scripts {
		if err := writeNew(sc.path, sc.raw, true); err != nil {
			return err
		}
	}
	return model.WithRegistryLock(storeDir, func() error {
		reg, err := model.LoadRegistry(storeDir)
		if err != nil {
			return err
		}
		shortcuts, err := shortcutsOf(pyos.Join(storeDir, "shortcuts.json"), reg)
		if err != nil {
			return err
		}
		for _, name := range entries.Keys {
			shortcuts.Set(name, entries.Vals[name])
		}
		return model.SaveRegistryAtomic(storeDir, reg)
	})
}

// applyNamespaces is apply_namespaces: under the registry lock, remove the namespaces being replaced and write the
// new files into imports/, each one new (never over a file that appeared since the review).
func applyNamespaces(storeDir string, plans []nsPlan, replaced []model.Group) error {
	if err := model.InitStore(storeDir); err != nil {
		return err
	}
	importsDir := pyos.Join(storeDir, "imports")
	return model.WithRegistryLock(storeDir, func() error {
		for _, g := range replaced {
			if err := removeNamespaceFiles(g); err != nil {
				return err
			}
		}
		for _, plan := range plans {
			for _, f := range plan.files {
				if err := writeNew(pyos.Join(importsDir, f.rel), f.raw, f.exec); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
