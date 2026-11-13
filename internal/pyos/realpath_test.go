// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (G1-2). Don't hand-edit without checking the plan.

package pyos

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// tree is the plan's 4.5 tree: directory t/real/sub, links t/l -> real, t/m -> real/sub, a -> b, b -> a, plus
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
	return e
}

// Expected values checked against Python 3.14 (the plan's section 4.5).
func TestRealpath_Vectors(t *testing.T) {
	e := tree(t)
	cases := []struct{ in, want string }{
		{"t/l/sub/../../l/sub", "t/real/sub"}, // a link resolved twice is not a loop
		{"t/m/..", "t/real"},                  // .. applies after the link's target
		{"t/l/", "t/real"},
		{"t/real/sub/../missing/../x", "t/real/x"}, // .. after a missing component is lexical
		{"a", "a"}, // a real loop leaves the rest unresolved
		{"a/x", "a/x"},
		{"t/real/sub/f.txt", "t/real/sub/f.txt"},
		{"t/lf", "t/real/sub/f.txt"},
		{"t/l/sub", "t/real/sub"},
		{"t/dead", "t/nowhere/deeper"},
		{"t/dead/x/../y", "t/nowhere/deeper/y"},
		{"t/abs/sub", "t/real/sub"},
		{"t/new/file.json", "t/new/file.json"},
		{"t//real/./sub/", "t/real/sub"},
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
	old, err := Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	if err := os.Chdir(filepath.Join(e.Tmp, "t/l")); err != nil { // through a link: the cwd is the real folder
		t.Fatal(err)
	}
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

func TestPaths_Normpath(t *testing.T) { // values from Python 3.14 posixpath.normpath
	for in, want := range map[string]string{
		"": ".", "/": "/", "//": "//", "///a": "/a", "//a//b": "//a/b", "a/../..": "..", "/a/../..": "/",
		"./a/./b/": "a/b", "a/b/../../..": "..", "../a": "../a", "a/../../b": "../b", "/..//x": "/x",
	} {
		if got := Normpath(in); got != want {
			t.Errorf("Normpath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPaths_Relpath(t *testing.T) { // values from Python 3.14 posixpath.relpath
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
		{"a/b", "a", true}, {"a", "/a", false}, {"/a/../b", "/a", true}, // pathlib does not look at ..
	} {
		if got := IsRelativeTo(c.path, c.other); got != c.want {
			t.Errorf("IsRelativeTo(%q, %q) = %v", c.path, c.other, got)
		}
	}
}

func TestPaths_JoinAndSplit(t *testing.T) { // posixpath.join / posixpath.split
	if pyJoin("", "a") != "a" || pyJoin("/", "a") != "/a" || pyJoin("a", "") != "a/" || pyJoin("a", "/b", "c") != "/b/c" {
		t.Error("pyJoin")
	}
	for in, want := range map[string][2]string{"/a": {"/", "a"}, "a/b/": {"a/b", ""}, "a//b": {"a", "b"}, "//a": {"//", "a"}, "a": {"", "a"}} {
		if h, tl := pySplit(in); h != want[0] || tl != want[1] {
			t.Errorf("pySplit(%q) = %q, %q", in, h, tl)
		}
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
