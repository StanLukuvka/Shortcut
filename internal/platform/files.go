package platform

import (
	"errors"
	"io/fs"
	"os"
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
