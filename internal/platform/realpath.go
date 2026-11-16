// Package platform is the part of the path, process, lock and JSON behaviour that shortcut depends on. Path
// functions are lexical ones (not filepath.Join); Realpath resolves symlinks without requiring the path to exist.
// Doc is one JSON object as written.
package platform

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// Realpath resolves every symlink in filename; parts that do not exist are kept as written. The unresolved parts
// are a stack; seen caches each link already resolved. A link is a loop only while it is still being resolved
// (value nil); meeting a resolved one again uses the cached path, so `t/l/sub/../../l/sub` works when t/l -> real.
// Resolution goes on after a loop, so `loop/../x` and `up/dang` still resolve. The result is absolute throughout: a
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
		if err != nil { // cwd gone: the path is only cleaned
			return filepath.Clean(filename)
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

// Getwd is the physical cwd from getcwd(2); os.Getwd prefers $PWD, which can name a different folder. Relative
// paths are resolved against the real folder so store keys and approval hashes do not depend on how the user
// got there. Nothing outside _test.go may call os.Getwd.
func Getwd() (string, error) { return syscall.Getwd() }

// Abs joins a relative path to the physical cwd, then cleans it.
func Abs(p string) string {
	if !strings.HasPrefix(p, "/") {
		// cwd gone: the path is only cleaned and stays relative.
		if cwd, err := Getwd(); err == nil {
			p = Join(cwd, p)
		}
	}
	return filepath.Clean(p)
}

// Relpath is the shortest ../ walk from start to path, both made absolute. An empty path is the cwd. A path pair Rel
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
