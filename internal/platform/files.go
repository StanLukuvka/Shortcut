package platform

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// stat answers whether path exists as want accepts it: false with a nil error only when nothing is there; any
// other error (permission denied, say) is returned, because then shortcut can't tell.
func stat(path string, follow bool, want func(os.FileInfo) bool) (bool, error) {
	st, err := os.Lstat(path)
	if follow {
		st, err = os.Stat(path)
	}
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return want(st), nil
}

// IsFile reports whether path is a regular file, links followed. An error other than "not there" is returned.
func IsFile(path string) (bool, error) {
	return stat(path, true, func(st os.FileInfo) bool { return st.Mode().IsRegular() })
}

// IsDir reports whether path is a folder, links followed. An error other than "not there" is returned.
func IsDir(path string) (bool, error) {
	return stat(path, true, os.FileInfo.IsDir)
}

// Exists reports whether anything is at path, links followed (a dangling link is not). An error other than "not there" is returned.
func Exists(path string) (bool, error) {
	return stat(path, true, func(os.FileInfo) bool { return true })
}

// Lexists reports whether anything is at path, a dangling link included. An error other than "not there" is returned.
func Lexists(path string) (bool, error) {
	return stat(path, false, func(os.FileInfo) bool { return true })
}

// IsLink reports whether path is a symlink. An error other than "not there" is returned.
func IsLink(path string) (bool, error) {
	return stat(path, false, func(st os.FileInfo) bool { return st.Mode()&os.ModeSymlink != 0 })
}

// CheckExecutable is nil when the current user may execute path, else the error that says how to fix it.
func CheckExecutable(path string) error {
	if syscall.Access(path, 1) != nil {
		return errors.New(path + " is not executable. Make it so: chmod +x " + ShellQuote(path))
	}
	return nil
}
