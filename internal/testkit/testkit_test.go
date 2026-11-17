package testkit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertRefused(t *testing.T, e *Env, outside string, env map[string]string) {
	t.Helper()
	o := Opts{Env: env}
	args := []string{"new", "x", "--", "true"}
	refused(t, "Run", func() { e.RunWith(o, args...) })
	refused(t, "InTerminal", func() { e.InTerminal(o, nil, args...) })
	refused(t, "Start", func() { e.Start(o, args...) })
	if n := len(entries(t, outside)); n != 0 {
		t.Errorf("outside has %d entries", n)
	}
}

func TestMain(m *testing.M) { Main(m) }

func TestFixtures_SandboxIsInsideTheTempDir(t *testing.T) {
	e := New(t)
	for _, name := range []string{"HOME", "SHORTCUTS_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "BIN_DIR"} {
		for _, v := range []string{e.Vars[name], os.Getenv(name)} {
			if !strings.HasPrefix(v, e.Tmp+"/") {
				t.Errorf("%s = %q, not under %s", name, v, e.Tmp)
			}
		}
	}
}

func TestFixtures_StoreDerivedFromTheOldHomeIsRefused(t *testing.T) {
	e, outside := sandboxed(t)
	// What `export HOME=$(mktemp -d) SHORTCUTS_HOME=$HOME/.shortcuts` leaves behind
	assertRefused(t, e, outside, map[string]string{"SHORTCUTS_HOME": outside + "/.shortcuts"})
}

func TestFixtures_HomeOutsideIsRefusedEvenWhenTheStoreIsInside(t *testing.T) {
	e, outside := sandboxed(t)
	assertRefused(t, e, outside, map[string]string{"HOME": outside})
}

func TestFixtures_HomeOutsideIsRefusedWhenTheStoreFollowsIt(t *testing.T) {
	e, outside := sandboxed(t)
	assertRefused(t, e, outside, map[string]string{"HOME": outside, "SHORTCUTS_HOME": ""})
}

func TestFixtures_EmptyHomeIsRefused(t *testing.T) {
	e, outside := sandboxed(t)
	assertRefused(t, e, outside, map[string]string{"HOME": ""})
}

func TestFixtures_TildeStoreFollowsTheChildHome(t *testing.T) {
	e, outside := sandboxed(t)
	assertRefused(t, e, outside, map[string]string{"HOME": outside, "SHORTCUTS_HOME": "~/.shortcuts"})
	// Inside the sandbox "~/elsewhere" is allowed through, still unexpanded: the child expands it.
	useBinary(t, e, `printf %s "$SHORTCUTS_HOME" > "$HOME/seen"`+"\n")
	res := e.RunWith(Opts{Env: map[string]string{"SHORTCUTS_HOME": "~/elsewhere"}}, "new", "x", "--", "true")
	if res.Code != 0 {
		t.Fatalf("exit %d: %s", res.Code, res.Stderr)
	}
	if got, _ := os.ReadFile(filepath.Join(e.Home, "seen")); string(got) != "~/elsewhere" {
		t.Errorf("child saw SHORTCUTS_HOME=%q", got)
	}
	// "~user" would be looked up in the real passwd database: refused.
	refused(t, "~user", func() { e.RunWith(Opts{Env: map[string]string{"BIN_DIR": "~root/bin"}}, "list") })
}

func TestFixtures_OtherPlacesARunWritesAreGuarded(t *testing.T) {
	e, outside := sandboxed(t)
	for _, name := range []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME", "BIN_DIR"} {
		assertRefused(t, e, outside, map[string]string{name: outside})
	}
}

func TestFixtures_PathThatOnlyLooksInsideIsRefused(t *testing.T) {
	e, outside := sandboxed(t)
	assertRefused(t, e, outside, map[string]string{"SHORTCUTS_HOME": e.Tmp + "-other"})
	assertRefused(t, e, outside, map[string]string{"SHORTCUTS_HOME": e.Tmp + "/../" + filepath.Base(outside)})
	link := filepath.Join(e.Tmp, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	assertRefused(t, e, outside, map[string]string{"SHORTCUTS_HOME": link + "/.shortcuts"})
}

func TestFixtures_CwdOutsideIsRefused(t *testing.T) {
	e, outside := sandboxed(t)
	refused(t, "Run", func() { e.RunWith(Opts{Cwd: outside}, "new", "x", "--local", "--", "true") })
	refused(t, "InTerminal", func() { e.InTerminal(Opts{Cwd: outside}, nil, "new", "x", "--local", "--", "true") })
	if n := len(entries(t, outside)); n != 0 {
		t.Errorf("outside has %d entries", n)
	}
}

func TestFixtures_ChangingTheSandboxEnvMidTestIsRefused(t *testing.T) {
	e, outside := sandboxed(t)
	e.Vars["SHORTCUTS_HOME"] = outside
	refused(t, "Run", func() { e.Run("list") })
}

func TestFixtures_ChangingTheProcessEnvMidTestIsRefused(t *testing.T) {
	e, outside := sandboxed(t)
	t.Setenv("HOME", outside)
	refused(t, "Run", func() { e.Run("list") })
}

func TestFixtures_SetupRefusesBeforeWritingAnything(t *testing.T) {
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	saved := sandboxedPaths
	t.Cleanup(func() { sandboxedPaths = saved })
	sandboxedPaths = append(append(sandboxedPaths[:0:0], saved...), struct{ Name, Default string }{"ELSEWHERE", outside})

	tmp := filepath.Join(t.TempDir(), "never-made")
	before := fmt.Sprint(os.Environ())
	e, err := newEnv(tmp)
	var se SandboxEscape
	if !errors.As(err, &se) || se.What != "ELSEWHERE" {
		t.Fatalf("newEnv: want a SandboxEscape for ELSEWHERE, got %v", err)
	}
	if _, err := os.Stat(e.Home); err == nil {
		t.Error("the home dir was made")
	}
	if fmt.Sprint(os.Environ()) != before {
		t.Error("the process env changed")
	}
}

func TestFixtures_PathsLeftUnsetFallUnderHome(t *testing.T) {
	e := New(t)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": e.Home}
	if err := RefuseUnlessSandboxed(e.Tmp, env, e.Home); err != nil {
		t.Fatal(err)
	}
	env["SHORTCUTS_HOME"] = outside
	var se SandboxEscape
	if err := RefuseUnlessSandboxed(e.Tmp, env, e.Home); !errors.As(err, &se) {
		t.Errorf("want a SandboxEscape, got %v", err)
	}
}

func TestBinary_BuildsTheModuleOnce(t *testing.T) {
	e := New(t)
	a, b := Binary(t), Binary(t)
	if a != b {
		t.Errorf("built twice: %s, %s", a, b)
	}
	if res := e.Run("--version"); res.Code != 0 || res.Stderr != "" {
		t.Errorf("--version: %+v", res)
	}
}

func TestNew_UnreadableDirsDontBreakCleanup(t *testing.T) {
	e := New(t)
	d := e.Folder("locked")
	e.WriteFile(filepath.Join(d, "f"), "x", 0)
	if err := os.Chmod(d, 0); err != nil {
		t.Fatal(err)
	}
}
