// Package osutil is the part of os.path, shlex and fcntl behaviour that shortcut depends on, so the answers
// are exact (path functions are lexical posixpath ones, not filepath's).
package osutil

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// Realpath is os.path.realpath(filename), strict=False, as CPython 3.14's iterative posixpath.realpath does it (the
// 3.10-3.12 recursive _joinrealpath stopped at the first loop; 3.14 keeps resolving the remaining parts, so
// `loop/../x` and `up/dang` after a loop differ between the two). The unresolved parts are a stack; seen caches each
// link already resolved. A link is a loop only while it is still being resolved (value nil); meeting a resolved one
// again uses the cached path, so `t/l/sub/../../l/sub` works when t/l -> real. The result is absolute throughout: a
// relative input starts from the PHYSICAL cwd (getcwd(2), never $PWD).
func Realpath(filename string) string {
	type part struct {
		s      string
		marker bool // the link whose path is the part below has been fully resolved
	}
	rest := strings.Split(filename, "/")
	slices.Reverse(rest)
	stack := make([]part, len(rest))
	for i, s := range rest {
		stack[i] = part{s: s}
	}
	count := len(stack)
	path := "/"
	if !strings.HasPrefix(filename, "/") {
		cwd, err := Getwd()
		if err != nil { // lazy: Python raises when the cwd is gone; here the path is only normalised. Upgrade: return an error.
			return Normpath(filename)
		}
		path = cwd
	}
	seen := map[string]*string{}
	for count > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if top.marker {
			link := stack[len(stack)-1].s
			stack = stack[:len(stack)-1]
			resolved := path
			seen[link] = &resolved
			continue
		}
		count--
		name := top.s
		switch name {
		case "", ".":
			continue
		case "..":
			if i := strings.LastIndex(path, "/"); i > 0 {
				path = path[:i]
			} else {
				path = "/"
			}
			continue
		}
		newpath := path + "/" + name
		if path == "/" {
			newpath = "/" + name
		}
		st, err := os.Lstat(newpath)
		if err != nil || st.Mode()&os.ModeSymlink == 0 { // missing or not a link: keep as is
			path = newpath
			continue
		}
		if cached, ok := seen[newpath]; ok {
			if cached != nil { // resolved before: reuse
				path = *cached
			} else { // still resolving: a loop; the link stays as it is and the rest goes on from it
				path = newpath
			}
			continue
		}
		target, err := os.Readlink(newpath)
		if err != nil {
			path = newpath
			continue
		}
		if strings.HasPrefix(target, "/") {
			path = "/"
		}
		seen[newpath] = nil
		stack = append(stack, part{s: newpath}, part{marker: true})
		tp := strings.Split(target, "/")
		for i := len(tp) - 1; i >= 0; i-- {
			stack = append(stack, part{s: tp[i]})
		}
		count += len(tp)
	}
	return path
}

// Getwd is the physical cwd: syscall.Getwd calls getcwd(2) and never reads $PWD, which os.Getwd prefers.
// Nothing outside _test.go may call os.Getwd.
func Getwd() (string, error) { return syscall.Getwd() }

// Abs is os.path.abspath: a relative path is joined to the physical cwd, then normalised.
func Abs(p string) string {
	if !strings.HasPrefix(p, "/") {
		// lazy: Python raises when the cwd is gone; here the path is only normalised, still relative. Upgrade: return an error.
		if cwd, err := Getwd(); err == nil {
			p = Join(cwd, p)
		}
	}
	return filepath.Clean(p)
}

// Normpath is filepath.Clean. lazy: kept only because Realpath's body stays unedited; callers use filepath.Clean.
func Normpath(p string) string { return filepath.Clean(p) }

// Relpath is posixpath.relpath(path, start): both made absolute, then the shortest ../ walk between them.
// lazy: Python raises ValueError for an empty path; here it is the cwd. Callers pass real paths. A path pair Rel
// cannot relate (only when the cwd is gone, so one side stayed relative) comes back as Abs(path).
func Relpath(path, start string) string {
	rel, err := filepath.Rel(Abs(start), Abs(path))
	if err != nil {
		return Abs(path)
	}
	return rel
}

// IsRelativeTo reports whether path is other or lies below it, lexically and component-wise (/a/bc is not under
// /a/b). filepath.Rel cleans both sides, so ".." components cannot climb out; a relative/absolute mix is an error.
func IsRelativeTo(path, other string) bool {
	rel, err := filepath.Rel(other, path)
	return err == nil && filepath.IsLocal(rel)
}

// Join is posixpath.join, not filepath.Join: a part starting with / restarts the path; join("", "a") = "a"; join("/", "a") = "/a";
// join("a", "") = "a/". store.go and store_write.go rely on the restart (an absolute script path stays as it
// is); filepath.Join would glue it under the folder.
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
