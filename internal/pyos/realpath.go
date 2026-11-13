// Package pyos is the part of Python's os, os.path, shlex and fcntl that shortcut's behaviour depends on, ported so
// the answers match Python's byte for byte (path functions are lexical posixpath ones, not filepath's).
package pyos

import (
	"os"
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

// Getwd is the physical cwd (C29): syscall.Getwd calls getcwd(2) and never reads $PWD, which os.Getwd prefers.
// Nothing outside _test.go may call os.Getwd.
func Getwd() (string, error) { return syscall.Getwd() }

// Abs is os.path.abspath: a relative path is joined to the physical cwd, then normalised.
func Abs(p string) string {
	if !strings.HasPrefix(p, "/") {
		// lazy: Python raises when the cwd is gone; here the path is only normalised, still relative. Upgrade: return an error.
		if cwd, err := Getwd(); err == nil {
			p = pyJoin(cwd, p)
		}
	}
	return Normpath(p)
}

// Normpath is posixpath.normpath: collapses //, /./ and a/../ lexically; "" is "."; a leading // is kept, /// is /.
func Normpath(p string) string {
	if p == "" {
		return "."
	}
	slashes := 0
	if strings.HasPrefix(p, "/") {
		slashes = 1
		if strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "///") {
			slashes = 2
		}
	}
	var comps []string
	for _, c := range strings.Split(p, "/") {
		if c == "" || c == "." {
			continue
		}
		if c != ".." || (slashes == 0 && len(comps) == 0) || (len(comps) > 0 && comps[len(comps)-1] == "..") {
			comps = append(comps, c)
		} else if len(comps) > 0 {
			comps = comps[:len(comps)-1]
		}
	}
	out := strings.Repeat("/", slashes) + strings.Join(comps, "/")
	if out == "" {
		return "."
	}
	return out
}

// Relpath is posixpath.relpath(path, start): both made absolute, then the shortest ../ walk between them.
// lazy: Python raises ValueError for an empty path; here it is the cwd. Callers pass real paths.
func Relpath(path, start string) string {
	startList := splitNonEmpty(Abs(start))
	pathList := splitNonEmpty(Abs(path))
	i := 0
	for i < len(startList) && i < len(pathList) && startList[i] == pathList[i] {
		i++
	}
	rel := make([]string, 0, len(startList)-i+len(pathList)-i)
	for j := i; j < len(startList); j++ {
		rel = append(rel, "..")
	}
	rel = append(rel, pathList[i:]...)
	if len(rel) == 0 {
		return "."
	}
	return strings.Join(rel, "/")
}

func splitNonEmpty(p string) []string {
	var out []string
	for _, c := range strings.Split(p, "/") {
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}

// IsRelativeTo is pathlib.Path.is_relative_to: component-wise, so /ab is not under /a. Like pathlib it drops "."
// and empty components but does not look at "..". lazy: a leading // (POSIX-special root) is treated as /.
func IsRelativeTo(path, other string) bool {
	if strings.HasPrefix(path, "/") != strings.HasPrefix(other, "/") {
		return false
	}
	a, b := pathParts(path), pathParts(other)
	if len(b) > len(a) {
		return false
	}
	for i := range b {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func pathParts(p string) []string {
	var out []string
	for _, c := range strings.Split(p, "/") {
		if c != "" && c != "." {
			out = append(out, c)
		}
	}
	return out
}

// pyJoin is posixpath.join: a part starting with / restarts the path; join("", "a") = "a"; join("/", "a") = "/a";
// join("a", "") = "a/".
func pyJoin(a string, parts ...string) string {
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

// pySplit is posixpath.split: split("/a") = ("/", "a"), split("a/b/") = ("a/b", ""); a head's trailing slashes are
// stripped unless it is all slashes.
func pySplit(p string) (head, tail string) {
	i := strings.LastIndex(p, "/") + 1
	head, tail = p[:i], p[i:]
	if head != "" && strings.Trim(head, "/") != "" {
		head = strings.TrimRight(head, "/")
	}
	return head, tail
}

// Join is posixpath.join, for callers outside this package.
func Join(a string, parts ...string) string { return pyJoin(a, parts...) }

// Dirname is posixpath.dirname.
func Dirname(p string) string { h, _ := pySplit(p); return h }

// Basename is posixpath.basename.
func Basename(p string) string { _, t := pySplit(p); return t }
