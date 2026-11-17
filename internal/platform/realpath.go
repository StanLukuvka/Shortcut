// Package platform is the part of the path, process, lock and JSON behaviour that shortcut depends on. Path
// functions are lexical ones (not filepath.Join); Realpath resolves symlinks and requires every part to exist.
package platform

import (
	"path/filepath"
	"strings"
	"syscall"
)

// Realpath resolves every symlink in filename, `..` after a link included. A part that does not exist, can't be read
// or is in a link loop is an error. A relative input starts from the PHYSICAL cwd (getcwd(2), never $PWD).
func Realpath(filename string) (string, error) {
	abs, err := absolute(filename)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// absolute joins a relative path to the physical cwd, without cleaning: `link/..` must still reach EvalSymlinks.
func absolute(p string) (string, error) {
	if strings.HasPrefix(p, "/") {
		return p, nil
	}
	cwd, err := Getwd()
	if err != nil {
		return "", err
	}
	return Join(cwd, p), nil
}

// Getwd is the physical cwd from getcwd(2); os.Getwd prefers $PWD, which can name a different folder. Relative
// paths are resolved against the real folder so a project's path does not depend on how the user got
// there. Nothing outside _test.go may call os.Getwd.
func Getwd() (string, error) { return syscall.Getwd() }

// Abs joins a relative path to the physical cwd, then cleans it. A relative path with no cwd is an error.
func Abs(p string) (string, error) {
	abs, err := absolute(p)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// Relpath is the shortest ../ walk from start to path, both made absolute. An empty path is the cwd.
func Relpath(path, start string) (string, error) {
	from, err := Abs(start)
	if err != nil {
		return "", err
	}
	to, err := Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Rel(from, to)
}

// IsRelativeTo reports whether path is other or lies below it, lexically and component-wise (/a/bc is not under
// /a/b). filepath.Rel cleans both sides, so ".." components cannot climb out; a relative/absolute mix is an error.
func IsRelativeTo(path, other string) bool {
	rel, err := filepath.Rel(other, path)
	return err == nil && filepath.IsLocal(rel)
}

// Join joins parts with "/". An absolute part restarts the path: a user-written script path or cwd is joined
// onto a folder, and an absolute one must stay as it is, where filepath.Join would glue it under the folder.
// Join("", "a") = "a"; Join("/", "a") = "/a"; Join("a", "") = "a/".
func Join(a string, parts ...string) string {
	path := a
	for _, b := range parts {
		switch {
		case strings.HasPrefix(b, "/"):
			path = b
		case path == "" || strings.HasSuffix(path, "/"):
			path += b
		default:
			path += "/" + b
		}
	}
	return path
}
