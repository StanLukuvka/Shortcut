package cli

import (
	"path/filepath"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// A source that ends in .git but starts with a dash is a path for git, never an option (Python lacks the `--`; deliberate).
func TestInit_LeadingDashGitSourceIsNotAnOption(t *testing.T) {
	e := testkit.New(t)
	marker := filepath.Join(e.Tmp, "pwned")
	res := e.Run("init", "--global", "--upload-pack=touch "+marker+";x.git", "-y")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "error: failed to clone git repository from --upload-pack=")
	notExist(t, marker)
}

// py: cli.py:100,122 keep an explicit empty --desc (`is not None`) on a plain new.
func TestNew_EmptyDescOnPlainNewIsKept(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "plain", "--desc", "")
	runOK(t, e, testkit.Opts{}, "new", "dflt")
	got := e.LoadGlobal()
	eq(t, "empty desc", got["plain"].(map[string]any)["description"], "")
	eq(t, "no desc", got["dflt"].(map[string]any)["description"], "(no description)")
}

// Python decodes argv with surrogateescape and stderr prints with backslashreplace; expected text is from Python.
func TestStderr_NonUTF8ArgvPrintsAsEscapes(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "hi", "--", "echo hi")
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"show", "\xff"}, 1, "error: shortcut '\\udcff' not found.\n"},
		{[]string{"--j\xff"}, 2, "error: unrecognized option '--j\\udcff'\nTry 'shortcut --help' for more information.\n"},
		{[]string{"list", "-", "\xff"}, 2, "usage: shortcut list [-h] [--names [NAMESPACE]]\nshortcut list: error: unrecognized arguments: - \\udcff\n"},
		{[]string{"show", "hi", "\xff"}, 1, "error: too many names: hi '\\udcff'\n"},
	} {
		res := e.Run(c.args...)
		if res.Code != c.code || res.Stdout != "" || res.Stderr != c.want {
			t.Errorf("%q: exit %d stdout %q stderr %q, want exit %d stderr %q", c.args, res.Code, res.Stdout, res.Stderr, c.code, c.want)
		}
	}
}

// py: cli.py:145,175 store `parsed.desc or "(no description)"`: an empty --desc on --copy counts as none.
func TestNew_EmptyDescOnCopyIsReplaced(t *testing.T) {
	e := testkit.New(t)
	script := e.Script("f.sh", "#!/bin/sh\necho hi\n")
	runOK(t, e, testkit.Opts{}, "new", "x", "--copy", script, "--desc", "")
	eq(t, "desc", e.LoadGlobal()["x"].(map[string]any)["description"], "(no description)")
}

// Python prints the "-> cwd" line on stderr with backslashreplace: a 0xff in the directory name is \udcff.
func TestRun_CwdLineEscapesNonUTF8(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"hello": inline("true")}, testkit.Name("p\xffroj"), testkit.Approved())
	res := e.RunWith(testkit.Opts{Cwd: proj}, "run", "hello")
	eq(t, "exit", res.Code, 0)
	contains(t, "stderr", res.Stderr, "p\\udcffroj")
}
