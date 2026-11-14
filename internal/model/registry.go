package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
)

// RegistryNotFoundError is the error for a missing registry file: a store without a registry is empty,
// but a named namespace file that is missing is a problem.
type RegistryNotFoundError struct{ Path string }

func (e *RegistryNotFoundError) Error() string { return "registry not found: " + e.Path }

// isJSONError: a problem with the file's text (what the decoder reports), as opposed to reading it (an *fs.PathError).
func isJSONError(err error) bool {
	var pe *fs.PathError
	return !errors.As(err, &pe)
}

// GlobalStoreDir returns $SHORTCUTS_HOME (when not empty) or ~/.shortcuts, made absolute.
func GlobalStoreDir() string {
	home := os.Getenv("SHORTCUTS_HOME")
	if home == "" {
		home = "~/.shortcuts"
	}
	return pyos.Abs(pyos.ExpandUser(home))
}

func RegistryExists(storeDir string) bool { return pyos.IsFile(pyos.Join(storeDir, "shortcuts.json")) }

// IsGlobalStore: Path(store_dir).resolve() == Path(global).resolve().
func IsGlobalStore(storeDir string) bool {
	return pyos.Realpath(storeDir) == pyos.Realpath(GlobalStoreDir())
}

// neverAProject: ~/.shortcuts is never a project, also when SHORTCUTS_HOME points the global store elsewhere.
func neverAProject(p string) bool {
	return p == pyos.Realpath(GlobalStoreDir()) || p == pyos.Realpath(pyos.Join(pyos.ExpandUser("~"), ProjectFile))
}

func isProjectRoot(folder string) bool {
	if neverAProject(pyos.Realpath(pyos.Join(folder, ProjectFile))) {
		return false
	}
	return pyos.IsFile(pyos.Join(folder, ProjectFile)) || pyos.IsFile(pyos.Join(folder, LocalFile))
}

// FindProjectRoot returns the nearest folder at or above start (default the cwd) with a .shortcuts or
// .shortcuts.local that is not the global store. "" = none.
func FindProjectRoot(start string) string {
	if start == "" {
		start, _ = pyos.Getwd()
	}
	for cur := pyos.Realpath(start); ; {
		if isProjectRoot(cur) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// oldProjectFolder returns the nearest .shortcuts folder above the caller that isn't the
// global store. "" = none.
func oldProjectFolder(callerCwd string) string {
	if callerCwd == "" {
		callerCwd, _ = pyos.Getwd()
	}
	for cur := pyos.Realpath(callerCwd); ; {
		path := pyos.Join(cur, ProjectFile)
		if pyos.IsDir(path) && !neverAProject(pyos.Realpath(path)) {
			return path
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// LocalProjectRoot returns where --local writes, this folder and never a parent. Its errors are
// the messages die prints.
func LocalProjectRoot(cwd string) (string, error) {
	here := pyos.Realpath(cwd)
	if root := FindProjectRoot(here); root != "" && root != here {
		return "", fmt.Errorf("%s is inside the project at %s. Run this from there.", here, root)
	}
	if neverAProject(pyos.Realpath(pyos.Join(here, ProjectFile))) {
		return "", fmt.Errorf("%s holds the global store, so it can't hold local shortcuts. Run this inside a project folder.", here)
	}
	if pyos.IsRelativeTo(here, pyos.Realpath(GlobalStoreDir())) {
		return "", fmt.Errorf("%s is inside the global store, so it can't hold local shortcuts. Run this inside a project folder.", here)
	}
	if st, err := os.Lstat(pyos.Join(here, LocalFile)); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is a symlink, so writing it would go somewhere else.", pyos.Join(here, LocalFile))
	}
	return here, nil
}

// lockPath is where the registry lock is: <store>/.lock for the global store; otherwise, to keep a repo's root clean,
// <global>/locks/<sha256(realpath(store))>.lock. The same names as the older version, so the two exclude each other.
func lockPath(storeDir string) string {
	if IsGlobalStore(storeDir) {
		return pyos.Join(storeDir, ".lock")
	}
	sum := sha256.Sum256([]byte(pyos.Realpath(storeDir)))
	return pyos.Join(GlobalStoreDir(), "locks", hex.EncodeToString(sum[:])+".lock")
}

// WithRegistryLock serializes read-modify-write of a store's registry across processes. Not reentrant: never nest
// it, and never call InitStore inside it.
func WithRegistryLock(storeDir string, fn func() error) error {
	return pyos.WithLock(lockPath(storeDir), fn)
}

// InitStore creates the global store's empty registry if it has none.
func InitStore(storeDir string) error {
	return WithRegistryLock(storeDir, func() error {
		if RegistryExists(storeDir) {
			return nil
		}
		return SaveRegistryAtomic(storeDir, emptyRegistry())
	})
}

func emptyRegistry() *pyjson.Object {
	o := &pyjson.Object{}
	o.Set("version", json.Number(strconv.Itoa(RegistryVersion)))
	o.Set("shortcuts", &pyjson.Object{})
	return o
}

// LoadRegistryFile reads a registry file, with fixed messages.
func LoadRegistryFile(path string) (*pyjson.Object, error) {
	if !pyos.IsFile(path) {
		return nil, &RegistryNotFoundError{path}
	}
	v, err := ReadJSONFile(path)
	if err != nil {
		if isJSONError(err) {
			return nil, fmt.Errorf("corrupt registry %s: %s", path, err)
		}
		return nil, err
	}
	data, ok := v.(*pyjson.Object)
	if !ok {
		return nil, fmt.Errorf("corrupt registry %s: root must be a JSON object", path)
	}
	version, present := data.Get("version")
	if !present {
		return nil, fmt.Errorf("%s needs \"version\": %d", path, RegistryVersion)
	}
	num, isNum := version.(json.Number)
	n, isInt := new(big.Int).SetString(string(num), 10) // a whole number as written: not 1.0, 1e0, a string or null
	if !isNum || !isInt {
		return nil, fmt.Errorf("corrupt registry %s: 'version' must be a whole number, such as %d", path, RegistryVersion)
	}
	switch n.Cmp(big.NewInt(RegistryVersion)) {
	case 1:
		return nil, fmt.Errorf("%s is format version %s, newer than this shortcut reads; upgrade shortcut", path, n)
	case -1:
		return nil, fmt.Errorf("%s needs \"version\": %d", path, RegistryVersion)
	}
	return data, nil
}

func LoadRegistry(storeDir string) (*pyjson.Object, error) {
	return LoadRegistryFile(pyos.Join(storeDir, "shortcuts.json"))
}

// SaveRegistryFile refuses to write over a file it can't read.
func SaveRegistryFile(path string, data *pyjson.Object) error {
	if pyos.IsFile(path) {
		if _, err := ReadJSONFile(path); err != nil {
			return fmt.Errorf("cannot overwrite corrupt registry %s: %s", path, err)
		}
	}
	return WriteJSONAtomic(path, data)
}

func SaveRegistryAtomic(storeDir string, data *pyjson.Object) error {
	if err := os.MkdirAll(storeDir, 0o777); err != nil {
		return err
	}
	return SaveRegistryFile(pyos.Join(storeDir, "shortcuts.json"), data)
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`) // Go's $ is end of text: no trailing newline, as fullmatch

// ValidateName checks a shortcut name, with a fixed message.
func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid shortcut name '%s'. Must match ^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$ "+
			"(alphanumeric start, up to 64 characters of letters, digits, underscores, and dashes).", name)
	}
	return nil
}

// ResolveScriptPath: absolute paths are used as given; relative ones resolve against baseDir and cannot escape it.
func ResolveScriptPath(baseDir, scriptField string) (string, error) {
	if strings.HasPrefix(scriptField, "/") {
		return scriptField, nil
	}
	base := pyos.Realpath(baseDir)
	target := pyos.Realpath(pyos.Join(base, scriptField))
	if !pyos.IsRelativeTo(target, base) {
		return "", fmt.Errorf("path traversal detected: '%s' resolves to '%s', outside '%s'.", scriptField, target, base)
	}
	return target, nil
}

// fileError returns what's wrong with a file as a whole ("" = nothing); EntryErrors checks what's inside
// 'shortcuts'. name is the namespace's, or "" for a main file.
func fileError(path string, data *pyjson.Object, name string) string {
	known := map[string]bool{"version": true, "description": true, "shortcuts": true}
	if name != "" {
		if _, has := data.Get("imports"); has {
			return path + " has its own 'imports', but namespaces don't nest"
		}
		if _, has := data.Get("shortcuts"); !has {
			return path + " needs 'shortcuts'"
		}
	} else {
		known["imports"] = true
	}
	if s, has := data.Get("shortcuts"); has {
		if _, isObj := s.(*pyjson.Object); !isObj {
			return fmt.Sprintf("corrupt registry %s: 'shortcuts' must be an object", path)
		}
	}
	if d, has := data.Get("description"); has {
		if _, isStr := d.(string); !isStr {
			return fmt.Sprintf("corrupt registry %s: 'description' must be a string", path)
		}
	}
	var unknown []string
	for _, k := range data.Keys {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return fmt.Sprintf("%s has an unknown field: %s", path, strings.Join(slices.Sorted(slices.Values(unknown)), ", "))
	}
	return ""
}
