// Package testkit is the hub of the Go tests: one sandbox per test, one set of
// fixtures, one way to start a process. Only this package may call t.TempDir, t.Setenv, os.Setenv or
// os.MkdirTemp, or import os/exec (internal/repocheck/rules_test.go enforces it); every launch is refused unless it stays inside
// the test's temp dir.
package testkit

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Env is one test's sandbox. Everything a test touches is under Tmp.
type Env struct {
	T       *testing.T
	Tmp     string            // filepath.EvalSymlinks(t.TempDir()): macOS /var is a link
	Home    string            // Tmp/home
	Store   string            // Home/.shortcuts, the value of SHORTCUTS_HOME
	ToolBin string            // Tmp/bin: where a `shortcut` link goes (Shell)
	Vars    map[string]string // HOME, SHORTCUTS_HOME, XDG_DATA_HOME, XDG_CONFIG_HOME, BIN_DIR over os.Environ()

	fail func(error) // test seam: testkit_test.go swaps t.Fatal for a panic to see a refusal
}

// What a run can write to besides HOME, and where it goes when the variable is unset or empty.
// A var so testkit_test.go can add an escaping entry.
var sandboxedPaths = []struct{ Name, Default string }{
	{"SHORTCUTS_HOME", "~/.shortcuts"},
	{"XDG_DATA_HOME", "~/.local/share"},
	{"XDG_CONFIG_HOME", "~/.config"},
	{"BIN_DIR", "~/.local/bin"},
}

// newEnv works out the sandbox for tmp and runs the guard. It writes nothing and sets nothing.
func newEnv(tmp string) (*Env, error) {
	e := &Env{Tmp: tmp, Home: filepath.Join(tmp, "home"), ToolBin: filepath.Join(tmp, "bin"), Vars: map[string]string{}}
	e.Store = filepath.Join(e.Home, ".shortcuts")
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			e.Vars[k] = v
		}
	}
	// PWD is per launch (launch sets it to the cwd): the test binary's own would be a stale one for the child.
	delete(e.Vars, "PWD")
	e.Vars["HOME"] = e.Home
	e.Vars["SHORTCUTS_HOME"] = e.Store
	e.Vars["XDG_DATA_HOME"] = filepath.Join(e.Home, ".local", "share")
	e.Vars["XDG_CONFIG_HOME"] = filepath.Join(e.Home, ".config")
	e.Vars["BIN_DIR"] = filepath.Join(e.Home, ".local", "bin")
	return e, RefuseUnlessSandboxed(tmp, e.Vars, e.Home)
}

// New makes the dirs for one test and sets the sandbox env in-process too (t.Setenv, so no t.Parallel).
// The guard runs before anything is written.
func New(t *testing.T) *Env {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e, err := newEnv(tmp)
	if err != nil {
		t.Fatal(err)
	}
	e.T = t
	// Runs before t.TempDir's own cleanup: a test may have left a dir unreadable.
	t.Cleanup(func() {
		filepath.WalkDir(tmp, func(p string, d fs.DirEntry, _ error) error {
			if d != nil && d.IsDir() {
				os.Chmod(p, 0o700)
			}
			return nil
		})
	})
	for _, k := range []string{"HOME", "SHORTCUTS_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "BIN_DIR"} {
		t.Setenv(k, e.Vars[k])
	}
	for _, d := range []string{e.Store, e.Vars["XDG_DATA_HOME"]} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// die ends the test with err (or hands it to the test seam).
func (e *Env) die(err error) {
	e.T.Helper()
	if e.fail != nil {
		e.fail(err)
	}
	e.T.Fatal(err)
}

func (e *Env) dief(format string, args ...any) {
	e.T.Helper()
	e.die(fmt.Errorf(format, args...))
}

// SandboxEscape: a test was about to read or write outside its temp dir.
type SandboxEscape struct{ What, Path, Root string }

func (s SandboxEscape) Error() string {
	if s.Path == "" {
		return fmt.Sprintf("%s is unset or empty, so a child would find the real one (temp dir %s)", s.What, s.Root)
	}
	return fmt.Sprintf("%s is %s, outside this test's temp dir %s", s.What, s.Path, s.Root)
}

// resolve makes p absolute and follows symlinks as far as the path exists; the rest is taken as it stands, and
// ".." is applied after the link before it, not lexically.
func resolve(p string) string {
	if !filepath.IsAbs(p) {
		if a, err := filepath.Abs(p); err == nil {
			p = a
		}
	}
	cur, rest := "/", strings.Split(p, "/")
	for links := 0; len(rest) > 0; {
		c := rest[0]
		rest = rest[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, c)
		fi, err := os.Lstat(next)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		if links++; links > 40 {
			return next
		}
		target, err := os.Readlink(next)
		if err != nil {
			cur = next
			continue
		}
		if filepath.IsAbs(target) {
			cur = "/"
		}
		rest = append(strings.Split(target, "/"), rest...)
	}
	return cur
}

func under(base, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return base + "/" + p // not Join: it would fold a ".." that a link before it makes mean something else
}

func refuseUnlessInside(root, what, path string) error {
	r := resolve(path)
	rootR := resolve(root)
	if r != rootR && !strings.HasPrefix(r, rootR+"/") {
		return SandboxEscape{what, r, rootR}
	}
	return nil
}

// RefuseUnlessSandboxed (= refuse_unless_sandboxed) returns a SandboxEscape unless HOME, the cwd and every path
// derived from them are inside root. Each path is worked out as the child will work it, from this env's own HOME,
// and compared after resolving links, so "..", symlinks and siblings such as <root>-other don't pass.
// A "~user" value is refused: the child would look it up in the real passwd database.
func RefuseUnlessSandboxed(root string, env map[string]string, cwd string) error {
	home := env["HOME"]
	if home == "" {
		return SandboxEscape{What: "HOME", Root: root}
	}
	base, err := filepath.Abs(cwd)
	if err != nil {
		return err
	}
	if err := refuseUnlessInside(root, "the working directory", base); err != nil {
		return err
	}
	if err := refuseUnlessInside(root, "HOME", under(base, home)); err != nil {
		return err
	}
	for _, s := range sandboxedPaths {
		v := env[s.Name]
		if v == "" {
			v = s.Default
		}
		switch {
		case v == "~" || strings.HasPrefix(v, "~/"):
			v = home + v[1:]
		case strings.HasPrefix(v, "~"):
			return SandboxEscape{s.Name + " (a ~user path)", v, root}
		}
		if err := refuseUnlessInside(root, s.Name, under(base, v)); err != nil {
			return err
		}
	}
	return nil
}

var (
	binOnce sync.Once
	binDir  string
	binPath string
	binErr  error
	// testBinary stands in for the built binary while testkit tests itself, so testkit's own tests
	// use `sh` scripts.
	testBinary string
)

// moduleRoot walks up from this source file to the directory holding go.mod.
func moduleRoot() string {
	_, file, _, _ := runtime.Caller(0)
	for d := filepath.Dir(file); ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		if d == filepath.Dir(d) {
			panic("testkit: no go.mod above " + file)
		}
	}
}

// Binary builds ./cmd/shortcut once per test process (CGO_ENABLED=0, the parent's env otherwise) and returns its path.
func Binary(t *testing.T) string {
	t.Helper()
	if testBinary != "" {
		return testBinary
	}
	binOnce.Do(func() {
		if binDir, binErr = os.MkdirTemp("", "shortcut-bin-"); binErr != nil {
			return
		}
		binPath = filepath.Join(binDir, "shortcut")
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/shortcut")
		cmd.Dir = moduleRoot()
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			binErr = fmt.Errorf("go build ./cmd/shortcut: %v\n%s", err, out)
		}
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return binPath
}

// Main is for TestMain: m.Run, then the built binary goes.
func Main(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		os.RemoveAll(binDir)
	}
	os.Exit(code)
}
