package platform

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// notThere is nil for "nothing is there": any other error (permission denied, say) is returned, because then
// shortcut can't tell.
func notThere(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// IsFile reports whether path is a regular file, links followed. An error other than "not there" is returned.
func IsFile(path string) (bool, error) {
	st, err := os.Stat(path)
	if err != nil {
		return false, notThere(err)
	}
	return st.Mode().IsRegular(), nil
}

// IsDir reports whether path is a folder, links followed. An error other than "not there" is returned.
func IsDir(path string) (bool, error) {
	st, err := os.Stat(path)
	if err != nil {
		return false, notThere(err)
	}
	return st.IsDir(), nil
}

// Exists reports whether anything is at path, links followed (a dangling link is not). An error other than "not there" is returned.
func Exists(path string) (bool, error) {
	_, err := os.Stat(path)
	return err == nil, notThere(err)
}

// Lexists reports whether anything is at path, a dangling link included. An error other than "not there" is returned.
func Lexists(path string) (bool, error) {
	_, err := os.Lstat(path)
	return err == nil, notThere(err)
}

// IsLink reports whether path is a symlink. An error other than "not there" is returned.
func IsLink(path string) (bool, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return false, notThere(err)
	}
	return st.Mode()&os.ModeSymlink != 0, nil
}

// CheckExecutable is nil when the current user may execute path, else the error that says how to fix it.
func CheckExecutable(path string) error {
	if syscall.Access(path, 1) != nil {
		return errors.New(path + " is not executable. Make it so: chmod +x " + ShellQuote(path))
	}
	return nil
}
