package pyos

import (
	"path/filepath"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// tree is a folder tree: directory t/real/sub, links t/l -> real, t/m -> real/sub, a -> b, b -> a, plus
// a file, a link to a file, a link to a missing target and a link with an absolute target.
func tree(t *testing.T) *testkit.Env {
	e := testkit.New(t)
	e.WriteFile(filepath.Join(e.Tmp, "t/real/sub/f.txt"), "x", 0)
	e.Symlink("real", filepath.Join(e.Tmp, "t/l"))
	e.Symlink("real/sub", filepath.Join(e.Tmp, "t/m"))
	e.Symlink("b", filepath.Join(e.Tmp, "a"))
	e.Symlink("a", filepath.Join(e.Tmp, "b"))
	e.Symlink("real/sub/f.txt", filepath.Join(e.Tmp, "t/lf"))
	e.Symlink("nowhere/deeper", filepath.Join(e.Tmp, "t/dead"))
	e.Symlink(filepath.Join(e.Tmp, "t/real"), filepath.Join(e.Tmp, "t/abs"))
	// the 3.14 cases: loops, `..` links, dangling links. w/up -> .. is the tree's root.
	e.WriteFile(filepath.Join(e.Tmp, "w/d/.keep"), "", 0)
	for _, l := range [][2]string{{"loopa", "w/loopa"}, {"..", "w/up"}, {"nowhere/x", "w/dang"}, {"c2", "w/c1"}, {"c1", "w/c2"},
		{"l2b", "w/loop"}, {"loop", "w/l2b"}, {"d", "w/l2"}, {"../w/d", "w/d/back"}, {"..", "w/d/up"}, {"d/up/w/d", "w/e"}} {
		e.Symlink(l[0], filepath.Join(e.Tmp, l[1]))
	}
	return e
}

// Expected values are posixpath.realpath's (CPython 3.14.10) on the same tree, relative to its root.
func TestRealpath_Vectors(t *testing.T) {
	e := tree(t)
	cases := []struct{ in, want string }{
		{"t/l/sub/../../l/sub", "t/real/sub"},
		{"t/m/..", "t/real"},
		{"t/l/", "t/real"},
		{"t/real/sub/../missing/../x", "t/real/x"},
		{"a", "a"},
		{"a/x", "a/x"},
		{"t/real/sub/f.txt", "t/real/sub/f.txt"},
		{"t/lf", "t/real/sub/f.txt"},
		{"t/l/sub", "t/real/sub"},
		{"t/dead", "t/nowhere/deeper"},
		{"t/dead/x/../y", "t/nowhere/deeper/y"},
		{"t/abs/sub", "t/real/sub"},
		{"t/new/file.json", "t/new/file.json"},
		{"t//real/./sub/", "t/real/sub"},
		{"w/loopa", "w/loopa"},
		{"w/loopa/x", "w/loopa/x"},
		{"w/./loopa/../up/dang", "dang"},
		{"w/loopa/../up/dang", "dang"},
		{"w/loopa/../t", "w/t"},
		{"w/loop//l2", "w/loop/l2"},
		{"w/loop/x/../l2/up", "w/loop/l2/up"},
		{"w/c1/..", "w"},
		{"w/c1/../c2/x", "w/c1/x"},
		{"w/up", "."},
		{"w/up/t/l", "t/real"},
		{"w/dang", "w/nowhere/x"},
		{"w/dang/../x", "w/nowhere/x"},
		{"w/d/up/t/m", "w/t/m"},
		{"w/d/back", "w/w/d"},
		{"w/d/up/w/d/up", "w/w/d/up"},
		{"w/e", "w/w/d"},
		{"w/e/up/up", "w/w/d/up/up"},
		{"w/l2/up/w/loopa/../t/l", "w/w/t/l"},
		{"w/loopa/loopa/../..", "w"},
		{"w/up/w/loopa/up", "w/loopa/up"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got, want := Realpath(e.Tmp+"/"+c.in), filepath.Join(e.Tmp, c.want); got != want {
				t.Errorf("Realpath(%q) = %q, want %q", c.in, got, want)
			}
		})
	}
}

func TestRealpath_RelativeInputUsesThePhysicalCwd(t *testing.T) {
	e := tree(t)
	t.Chdir(filepath.Join(e.Tmp, "t/l")) // through a link: the cwd is the real folder
	want := filepath.Join(e.Tmp, "t/real")
	if cwd, _ := Getwd(); cwd != want {
		t.Fatalf("Getwd() = %q, want the physical %q", cwd, want)
	}
	for in, want := range map[string]string{
		"sub":        want + "/sub",
		"../m/../f":  want + "/f",
		"./sub/../x": want + "/x",
		"../../a":    e.Tmp + "/a",
	} {
		if got := Realpath(in); got != want {
			t.Errorf("Realpath(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Abs("x/../y"); got != want+"/y" {
		t.Errorf("Abs = %q", got)
	}
}

func TestPaths_Normpath(t *testing.T) { // filepath.Clean; differs from posixpath.normpath only for a leading //
	for in, want := range map[string]string{
		"": ".", "/": "/", "//": "/", "///a": "/a", "//a//b": "/a/b", "a/../..": "..", "/a/../..": "/",
		"./a/./b/": "a/b", "a/b/../../..": "..", "../a": "../a", "a/../../b": "../b", "/..//x": "/x",
	} {
		if got := Normpath(in); got != want {
			t.Errorf("Normpath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPaths_Relpath(t *testing.T) { // values are posixpath.relpath's (CPython 3.14)
	for _, c := range [][3]string{
		{"/a/b", "/a", "b"}, {"/a", "/a/b", ".."}, {"/a/b/c", "/a/x/y", "../../b/c"}, {"/a", "/a", "."}, {"/a/b", "/", "a/b"},
	} {
		if got := Relpath(c[0], c[1]); got != c[2] {
			t.Errorf("Relpath(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestPaths_IsRelativeTo(t *testing.T) {
	for _, c := range []struct {
		path, other string
		want        bool
	}{
		{"/a/b", "/a", true}, {"/a", "/a", true}, {"/ab", "/a", false}, {"/a", "/a/b", false}, {"/a/./b//c", "/a/b", true},
		{"a/b", "a", true}, {"a", "/a", false},
		{"/a/../b", "/a", false}, {"/a/b/../../x", "/a", false}, {"/a/b/..", "/a/b", false}, {"/a/b/../c", "/a", true},
		{"/a/bc", "/a/b", false}, {"/a/b", "/a/b/", true}, {"/", "/a", false}, {"/a", "/", true}, {"/a/..", "/a", false},
	} {
		if got := IsRelativeTo(c.path, c.other); got != c.want {
			t.Errorf("IsRelativeTo(%q, %q) = %v", c.path, c.other, got)
		}
	}
}

func TestPaths_Join(t *testing.T) { // posixpath.join: an absolute part restarts the path
	if Join("", "a") != "a" || Join("/", "a") != "/a" || Join("a", "") != "a/" || Join("a", "/b", "c") != "/b/c" {
		t.Error("Join")
	}
}

func TestExpandUser(t *testing.T) {
	e := testkit.New(t) // HOME is e.Home
	for in, want := range map[string]string{
		"~": e.Home, "~/x": e.Home + "/x", "x/~": "x/~", "": "", "~nosuchuser_zz9/x": "~nosuchuser_zz9/x",
	} {
		if got := ExpandUser(in); got != want {
			t.Errorf("ExpandUser(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ExpandUser("~root"); got == "~root" || got == "" { // an account that exists on every unix
		t.Errorf("ExpandUser(~root) = %q", got)
	}
}
