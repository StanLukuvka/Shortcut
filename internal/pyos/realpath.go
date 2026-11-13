// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (G1-2). Don't hand-edit without checking the plan.

// Package pyos is the part of Python's os, os.path, shlex and fcntl that shortcut's behaviour depends on, ported so
// the answers match Python's byte for byte (path functions are lexical posixpath ones, not filepath's).
package pyos

import (
	"os"
	"strings"
	"syscall"
)

// Realpath is os.path.realpath(filename), strict=False, as posixpath._joinrealpath does it (3.10-3.12; 3.13+ is an
// iterative rewrite with the same results). seen caches each link already resolved. A link is a loop only while it
// is still being resolved (value nil); meeting a resolved one again uses the cached path, so
// `t/l/sub/../../l/sub` works when t/l -> real.
func Realpath(filename string) string {
	p, _ := joinRealpath("", filename, map[string]*string{})
	return Abs(p) // Python: abspath(path); a relative input is made absolute only at the end, against the PHYSICAL cwd
}

func joinRealpath(path, rest string, seen map[string]*string) (string, bool) {
	if strings.HasPrefix(rest, "/") {
		rest = rest[1:]
		path = "/"
	}
	for rest != "" {
		var name string
		name, rest, _ = strings.Cut(rest, "/")
		switch name {
		case "", ".":
			continue
		case "..":
			if path != "" {
				var base string
				path, base = pySplit(path)
				if base == ".." {
					path = pyJoin(path, "..", "..")
				}
			} else {
				path = ".."
			}
			continue // lexical, also after a missing component, as Python does
		}
		newpath := pyJoin(path, name)
		st, err := os.Lstat(newpath)
		if err != nil || st.Mode()&os.ModeSymlink == 0 { // missing or not a link: keep lexically
			path = newpath
			continue
		}
		if cached, ok := seen[newpath]; ok {
			if cached != nil { // resolved before: reuse
				path = *cached
				continue
			}
			return pyJoin(newpath, rest), false // still resolving: a loop; Python leaves the rest as is
		}
		seen[newpath] = nil // not resolved yet
		target, err := os.Readlink(newpath)
		if err != nil {
			path = newpath
			continue
		}
		var ok bool
		path, ok = joinRealpath(path, target, seen) // the target is resolved from the folder holding the link
		if !ok {
			return pyJoin(path, rest), false
		}
		resolved := path
		seen[newpath] = &resolved
	}
	return path, true
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
