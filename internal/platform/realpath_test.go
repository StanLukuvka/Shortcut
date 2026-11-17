package platform

import (
	"errors"
	"os"
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

// Expected values were worked out by hand on this tree, relative to its root.
func TestRealpath_Vectors(t *testing.T) {
	e := tree(t)
	cases := []struct{ in, want string }{
		{"t/l/sub/../../l/sub", "t/real/sub"},
		{"t/m/..", "t/real"},
		{"t/l/", "t/real"},
		{"t/real/sub/f.txt", "t/real/sub/f.txt"},
		{"t/lf", "t/real/sub/f.txt"},
		{"t/l/sub", "t/real/sub"},
		{"t/abs/sub", "t/real/sub"},
		{"t//real/./sub/", "t/real/sub"},
		{"w/up", "."},
		{"w/up/t/l", "t/real"},
		{"w/d/up", "w"},
		{"w/d/up/d/up/d", "w/d"},
		{"w/l2/up/..", "."},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got, err := Realpath(e.Tmp + "/" + c.in); err != nil || got != filepath.Join(e.Tmp, c.want) {
				t.Errorf("Realpath(%q) = %q, %v, want %q", c.in, got, err, filepath.Join(e.Tmp, c.want))
			}
		})
	}
}

// A part that is missing, a dangling link, and a loop are errors: nothing is kept as written. These were
// resolved by guessing before.
func TestRealpath_ErrorsInsteadOfGuessing(t *testing.T) {
	e := tree(t)
	for _, in := range []string{
		"t/real/sub/../missing/../x", "t/new/file.json", "t/dead", "t/dead/x/../y", // missing, dangling
		"a", "a/x", "b", "w/loopa", "w/loopa/x", "w/c1/..", "w/loop//l2", // loops
		"w/./loopa/../up/dang", "w/dang", "w/dang/../x", "w/up/w/loopa/up",
		"w/d/up/t/m", "w/d/back", "w/e", // through links to folders that are not there
	} {
		t.Run(in, func(t *testing.T) {
			if got, err := Realpath(e.Tmp + "/" + in); err == nil {
				t.Errorf("Realpath(%q) = %q, want an error", in, got)
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
		"sub":                want + "/sub",
		"../m/..":            want,
		"./sub/../sub/f.txt": want + "/sub/f.txt",
		"../..":              e.Tmp,
	} {
		if got, err := Realpath(in); err != nil || got != want {
			t.Errorf("Realpath(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	if _, err := Realpath("../m/../f"); err == nil {
		t.Error("a path to a file that is not there resolved")
	}
}

func TestRealpath_NoCwdIsAnError(t *testing.T) {
	e := testkit.New(t)
	gone := filepath.Join(e.Tmp, "gone")
	e.WriteFile(filepath.Join(gone, ".keep"), "", 0)
	t.Chdir(gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if got, err := Realpath("x"); err == nil {
		t.Errorf("Realpath(x) = %q with no cwd", got)
	}
}

func TestPaths_Relpath(t *testing.T) {
	for _, c := range [][3]string{
		{"/a/b", "/a", "b"}, {"/a", "/a/b", ".."}, {"/a/b/c", "/a/x/y", "../../b/c"}, {"/a", "/a", "."}, {"/a/b", "/", "a/b"},
	} {
		if got, err := Relpath(c[0], c[1]); err != nil || got != c[2] {
			t.Errorf("Relpath(%q, %q) = %q, %v, want %q", c[0], c[1], got, err, c[2])
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

func TestPaths_Join(t *testing.T) { // an absolute part restarts the path
	if Join("", "a") != "a" || Join("/", "a") != "/a" || Join("a", "") != "a/" || Join("a", "/b", "c") != "/b/c" {
		t.Error("Join")
	}
}

func TestExpandUser(t *testing.T) {
	e := testkit.New(t) // HOME is e.Home
	for in, want := range map[string]string{
		"~": e.Home, "~/x": e.Home + "/x", "x/~": "x/~", "": "", "~bob/x": "~bob/x", "~root": "~root",
	} {
		if got, err := ExpandUser(in); err != nil || got != want {
			t.Errorf("ExpandUser(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	home := func(h string, err error) func() (string, error) { return func() (string, error) { return h, err } }
	for _, bad := range []error{errors.New("$HOME is not defined")} { // os.UserHomeDir fails on unset or empty HOME
		if got, err := expandWith("~/x", home("", bad)); err == nil {
			t.Errorf("no HOME: %q", got)
		}
	}
	if got, err := expandWith("~/x", home("rel", nil)); err == nil { // a relative HOME
		t.Errorf("relative HOME: %q", got)
	}
	if got, err := expandWith("x/~", home("", errors.New("unset"))); err != nil || got != "x/~" { // no "~" to expand: HOME is not read
		t.Errorf("no tilde: %q, %v", got, err)
	}
	if got, err := expandWith("~/x", home("/", nil)); err != nil || got != "/x" {
		t.Errorf("HOME=/: %q, %v", got, err)
	}
	if got, err := expandWith("~", home("/", nil)); err != nil || got != "/" {
		t.Errorf("~ with HOME=/: %q, %v", got, err)
	}
}
