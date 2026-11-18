package testkit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Shortcuts for a store. Writers only read these, so tests can share them.
var (
	HI      = map[string]any{"hi": map[string]any{"inline": "echo hi"}}
	GO      = map[string]any{"go": map[string]any{"inline": "echo ran"}}
	OWN     = map[string]any{"own": map[string]any{"inline": "echo own"}}
	BinTool = map[string]any{"tool": map[string]any{"script": "bin/tool"}}

	// Whole .shortcut files, for WriteImport.
	DockerPS = map[string]any{"version": 1, "shortcuts": map[string]any{"ps": map[string]any{"inline": "echo ps"}}}
	KubeGet  = map[string]any{"version": 1, "shortcuts": map[string]any{"get": map[string]any{"inline": "echo get"}}}
)

const brokenJSON = "{not json"

// jsonText is the JSON text of v: keys come out sorted (use WriteRegistryRaw where order is the point),
// and non-ASCII and <>& stay as they are.
func (e *Env) jsonText(v any) string {
	e.T.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		e.die(err)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func (e *Env) inside(what, path string) {
	e.T.Helper()
	if err := refuseUnlessInside(e.Tmp, what, path); err != nil {
		e.die(err)
	}
}

// Files and folders. Paths outside Tmp are refused.

// WriteFile writes a file and any folders above it, inside Tmp; mode 0 = 0644.
func (e *Env) WriteFile(path string, content string, mode os.FileMode) string {
	e.T.Helper()
	e.inside("a fixture file", path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.die(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		e.die(err)
	}
	if mode != 0 {
		if err := os.Chmod(path, mode); err != nil {
			e.die(err)
		}
	}
	return path
}

func (e *Env) writeFiles(root string, files map[string]string, mode os.FileMode) {
	e.T.Helper()
	for rel, content := range files {
		e.WriteFile(filepath.Join(root, rel), content, mode)
	}
}

// Script is an executable in Tmp, outside any store.
func (e *Env) Script(name, content string) string {
	e.T.Helper()
	return e.WriteFile(filepath.Join(e.Tmp, name), content, 0o755)
}

func (e *Env) Folder(name string) string {
	e.T.Helper()
	p := filepath.Join(e.Tmp, name)
	if err := os.Mkdir(p, 0o755); err != nil {
		e.die(err)
	}
	return p
}

// Symlink makes link (inside Tmp) point at target, which may be anywhere.
func (e *Env) Symlink(target, link string) {
	e.T.Helper()
	e.inside("a fixture link", link)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		e.die(err)
	}
	if err := os.Symlink(target, link); err != nil {
		e.die(err)
	}
}

// Stores. storeDir is a project's .shortcuts file, or a folder holding
// shortcuts.json (the global store included).

// WriteRegistry writes exactly this data as a store's file, valid or not.
func (e *Env) writeRegistry(storeDir string, data any) {
	e.T.Helper()
	e.WriteFile(e.registryPath(storeDir), e.jsonText(data), 0)
}

func (e *Env) WriteRegistryRaw(storeDir, jsonText string) {
	e.T.Helper()
	e.WriteFile(e.registryPath(storeDir), jsonText, 0)
}

// registryPath is a project's .shortcuts file, or <dir>/shortcuts.json.
func (e *Env) registryPath(storeDir string) string {
	if filepath.Base(storeDir) == ".shortcuts" && filepath.Clean(storeDir) != e.Store {
		return storeDir
	}
	return filepath.Join(storeDir, "shortcuts.json")
}

func (e *Env) WriteStore(storeDir string, shortcuts map[string]any, extra ...map[string]any) {
	e.T.Helper()
	if shortcuts == nil {
		shortcuts = map[string]any{}
	}
	data := map[string]any{"version": 1, "shortcuts": shortcuts}
	for _, x := range extra {
		for k, v := range x {
			data[k] = v
		}
	}
	e.writeRegistry(storeDir, data)
}

// WriteImport writes a namespace file. A project lists it under "imports" in .shortcuts; the global store just finds it.
func (e *Env) WriteImport(storeDir, name string, data map[string]any) string {
	e.T.Helper()
	if filepath.Clean(storeDir) == e.Store || filepath.Base(storeDir) != ".shortcuts" {
		return e.WriteFile(filepath.Join(storeDir, "imports", name+".shortcut"), e.jsonText(data), 0)
	}
	path := e.WriteFile(filepath.Join(filepath.Dir(storeDir), "imports", name+".shortcut"), e.jsonText(data), 0)
	raw, err := os.ReadFile(storeDir)
	if err != nil {
		e.die(err)
	}
	var reg map[string]any
	if err := json.Unmarshal(raw, &reg); err != nil {
		e.die(err)
	}
	imports, _ := reg["imports"].(map[string]any)
	if imports == nil {
		imports = map[string]any{}
	}
	imports[name] = "imports/" + name + ".shortcut"
	reg["imports"] = imports
	e.WriteFile(storeDir, e.jsonText(reg), 0)
	return path
}

func (e *Env) CorruptStore(storeDir string) {
	e.T.Helper()
	e.WriteRegistryRaw(storeDir, brokenJSON)
}

// LoadGlobal is the "shortcuts" object of the global shortcuts.json.
func (e *Env) LoadGlobal() map[string]any {
	e.T.Helper()
	raw, err := os.ReadFile(filepath.Join(e.Store, "shortcuts.json"))
	if err != nil {
		e.die(err)
	}
	var reg struct {
		Shortcuts map[string]any `json:"shortcuts"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		e.die(err)
	}
	return reg.Shortcuts
}

// Projects and collections.

type projectCfg struct {
	name  string
	dirs  []string
	files map[string]string
	extra map[string]any
	mode  os.FileMode
}

type ProjectOpt func(*projectCfg)

func Name(n string) ProjectOpt             { return func(c *projectCfg) { c.name = n } }
func Dirs(d ...string) ProjectOpt          { return func(c *projectCfg) { c.dirs = d } }
func Files(f map[string]string) ProjectOpt { return func(c *projectCfg) { c.files = f } }
func Extra(x map[string]any) ProjectOpt    { return func(c *projectCfg) { c.extra = x } }
func Mode(m os.FileMode) ProjectOpt        { return func(c *projectCfg) { c.mode = m } } // mode of the Files

func cfgOf(name string, opts []ProjectOpt) *projectCfg {
	c := &projectCfg{name: name}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Project is a project folder in Tmp, with a .shortcuts store if given shortcuts (nil = none).
// Dirs and Files are relative to the project; Extra goes in the store's JSON.
func (e *Env) Project(shortcuts map[string]any, opts ...ProjectOpt) string {
	e.T.Helper()
	c := cfgOf("proj", opts)
	proj := filepath.Join(e.Tmp, c.name)
	if err := os.Mkdir(proj, 0o755); err != nil {
		e.die(err)
	}
	for _, d := range c.dirs {
		if err := os.MkdirAll(filepath.Join(proj, d), 0o755); err != nil {
			e.die(err)
		}
	}
	e.writeFiles(proj, c.files, c.mode)
	if shortcuts != nil {
		e.WriteStore(filepath.Join(proj, ".shortcuts"), shortcuts, c.extra)
	}
	return proj
}

// BrokenProject is a project whose store can't be parsed.
func (e *Env) BrokenProject() string {
	e.T.Helper()
	proj := e.Project(nil)
	e.CorruptStore(filepath.Join(proj, ".shortcuts"))
	return proj
}
