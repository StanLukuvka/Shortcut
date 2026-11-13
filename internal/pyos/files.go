package pyos

import (
	"os"
	"path/filepath"
	"strings"
)

// IsFile is os.path.isfile: a regular file, links followed.
func IsFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// IsDir is os.path.isdir: a folder, links followed.
func IsDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// Exists is os.path.exists: anything there, links followed (a dangling link is not).
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Lexists is os.path.lexists: anything there, a dangling link included.
func Lexists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// IsLink reports whether path is a symlink.
func IsLink(path string) bool {
	st, err := os.Lstat(path)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

// Suffix is pathlib.PurePath(p).suffix as Python 3.14 has it: from the last dot of the file name, unless that is
// its first character ('.bashrc' has none); 'a.' has '.'.
func Suffix(p string) string {
	base := filepath.Base(filepath.Clean(p))
	if base == ".." || (strings.HasPrefix(base, ".") && strings.Count(base, ".") == 1) {
		return ""
	}
	return filepath.Ext(base)
}
