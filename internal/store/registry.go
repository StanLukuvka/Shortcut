package store

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math/big"
	"regexp"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/platform"
)

// isJSONError: a problem with the file's text (what the decoder reports), as opposed to reading it (an *fs.PathError).
func isJSONError(err error) bool {
	var pe *fs.PathError
	return !errors.As(err, &pe)
}

// LoadRegistryFile reads a registry file, with fixed messages.
func LoadRegistryFile(path string) (platform.Doc, error) {
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

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`) // $ matches only at the very end of the text (no multiline flag), so a trailing newline fails

// ValidateName checks a shortcut name, with a fixed message.
func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid shortcut name '%s'. Must match ^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$ "+
			"(alphanumeric start, up to 64 characters of letters, digits, underscores, and dashes).", name)
	}
	return nil
}

// ScriptPath is where an entry's script is: an absolute path as given, a relative one joined to baseDir (not
// cleaned, so the OS resolves a `..` after a link as it does for any path).
func ScriptPath(baseDir, scriptField string) string {
	if strings.HasPrefix(scriptField, "/") {
		return scriptField
	}
	return platform.Join(baseDir, scriptField)
}

// fileError returns what's wrong with a file as a whole ("" = nothing); EntryErrors checks what's inside
// 'shortcuts'. name is the namespace's, or "" for a main file.
func fileError(path string, data platform.Doc, name string) string {
	known := map[string]bool{}
	for _, k := range FileKeys() {
		known[k] = true
	}
	if name != "" {
		if data.Has("imports") {
			return path + " has its own 'imports', but namespaces don't nest"
		}
		if !data.Has("shortcuts") {
			return path + " needs 'shortcuts'"
		}
		delete(known, "imports")
	}
	if _, isObj := data.Object("shortcuts"); data.Has("shortcuts") && !isObj {
		return fmt.Sprintf("corrupt registry %s: 'shortcuts' must be an object", path)
	}
	if _, isStr := data.String("description"); data.Has("description") && !isStr {
		return fmt.Sprintf("corrupt registry %s: 'description' must be a string", path)
	}
	unknown := slices.DeleteFunc(slices.Sorted(maps.Keys(data)), func(k string) bool { return known[k] })
	if len(unknown) > 0 {
		return fmt.Sprintf("%s has an unknown field: %s", path, strings.Join(unknown, ", "))
	}
	return ""
}
