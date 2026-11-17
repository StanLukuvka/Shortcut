package store

import (
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/platform"
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

// LoadRegistryFile reads a registry file, with fixed messages.
func LoadRegistryFile(path string) (platform.Doc, error) {
	isFile, err := platform.IsFile(path)
	if err != nil {
		return nil, err
	}
	if !isFile {
		return nil, &RegistryNotFoundError{path}
	}
	text, err := platform.ReadJSONFile(path)
	var data platform.Doc
	if err == nil {
		data, err = platform.ParseDoc(text)
	}
	if err != nil {
		if isJSONError(err) {
			return nil, fmt.Errorf("corrupt registry %s: %s", path, err)
		}
		return nil, err
	}
	version, present := data["version"]
	if !present {
		return nil, fmt.Errorf("%s needs \"version\": %d", path, RegistryVersion)
	}
	n, isInt := new(big.Int).SetString(string(version), 10) // a whole number as written: not 1.0, 1e0, a string or null
	if !isInt {
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

func LoadRegistry(storeDir string) (platform.Doc, error) {
	return LoadRegistryFile(platform.Join(storeDir, "shortcuts.json"))
}

// SaveRegistryFile refuses to write over a file it can't read.
func SaveRegistryFile(path string, data platform.Doc) error {
	isFile, err := platform.IsFile(path)
	if err != nil {
		return err
	}
	if isFile {
		if _, err := platform.ReadJSONFile(path); err != nil {
			return fmt.Errorf("cannot overwrite corrupt registry %s: %s", path, err)
		}
	}
	return platform.WriteJSONAtomic(path, data)
}

func SaveRegistryAtomic(storeDir string, data platform.Doc) error {
	if err := os.MkdirAll(storeDir, 0o777); err != nil {
		return err
	}
	return SaveRegistryFile(platform.Join(storeDir, "shortcuts.json"), data)
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`) // $ matches only at the very end of the text (no multiline flag), so a trailing newline fails

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
	base, err := platform.Realpath(baseDir)
	if err != nil {
		return "", err
	}
	target := platform.Join(base, scriptField)
	there, err := platform.Exists(target)
	if err != nil {
		return "", err
	}
	if there {
		target, err = platform.Realpath(target)
	} else { // a script that is not there is reported by its caller ("script file not found"): its folder must be
		dir, name := filepath.Split(target) // not Dir: it would clean away a `..` that follows a link
		if dir, err = platform.Realpath(dir); err == nil {
			target = platform.Join(dir, name)
		}
	}
	if err != nil {
		return "", err
	}
	if !platform.IsRelativeTo(target, base) {
		return "", fmt.Errorf("path traversal detected: '%s' resolves to '%s', outside '%s'.", scriptField, target, base)
	}
	return target, nil
}

// fileError returns what's wrong with a file as a whole ("" = nothing); EntryErrors checks what's inside
// 'shortcuts'. name is the namespace's, or "" for a main file.
func fileError(path string, data platform.Doc, name string) string {
	known := map[string]bool{"version": true, "description": true, "shortcuts": true}
	if name != "" {
		if data.Has("imports") {
			return path + " has its own 'imports', but namespaces don't nest"
		}
		if !data.Has("shortcuts") {
			return path + " needs 'shortcuts'"
		}
	} else {
		known["imports"] = true
	}
	if _, isObj := data.Object("shortcuts"); data.Has("shortcuts") && !isObj {
		return fmt.Sprintf("corrupt registry %s: 'shortcuts' must be an object", path)
	}
	if _, isStr := data.String("description"); data.Has("description") && !isStr {
		return fmt.Sprintf("corrupt registry %s: 'description' must be a string", path)
	}
	var unknown []string
	for k := range data {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return fmt.Sprintf("%s has an unknown field: %s", path, strings.Join(slices.Sorted(slices.Values(unknown)), ", "))
	}
	return ""
}
