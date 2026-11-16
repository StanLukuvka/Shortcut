package platform

import (
	"errors"
	"os"
	"syscall"
)

// IsFile reports whether path is a regular file, links followed.
func IsFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// IsDir reports whether path is a folder, links followed.
func IsDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// Exists reports whether anything is at path, links followed (a dangling link is not).
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Lexists reports whether anything is at path, a dangling link included.
func Lexists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// IsLink reports whether path is a symlink.
func IsLink(path string) bool {
	st, err := os.Lstat(path)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

// CheckExecutable is nil when the current user may execute path, else the error that says how to fix it.
func CheckExecutable(path string) error {
	if syscall.Access(path, 1) != nil {
		return errors.New(path + " is not executable. Make it so: chmod +x " + ShellQuote(path))
	}
	return nil
}
