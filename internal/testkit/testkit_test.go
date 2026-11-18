package testkit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertRefused(t *testing.T, e *Env, outside string, o Opts) {
	t.Helper()
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

// Every way a child's environment or cwd could reach outside the sandbox is refused, by Run, InTerminal and Start.
func TestFixtures_AnythingReachingOutsideTheSandboxIsRefused(t *testing.T) {
	envOf := func(k, v string) func(e *Env, outside string) Opts {
		return func(e *Env, outside string) Opts { return Opts{Env: map[string]string{k: v}} }
	}
	rows := []struct {
		name string
		opts func(e *Env, outside string) Opts
	}{
		// What `export HOME=$(mktemp -d) SHORTCUTS_HOME=$HOME/.shortcuts` leaves behind
		{"store derived from the old home", func(e *Env, o string) Opts {
			return Opts{Env: map[string]string{"SHORTCUTS_HOME": o + "/.shortcuts"}}
		}},
		{"home outside, store inside", func(e *Env, o string) Opts { return Opts{Env: map[string]string{"HOME": o}} }},
		{"home outside, store follows", func(e *Env, o string) Opts {
			return Opts{Env: map[string]string{"HOME": o, "SHORTCUTS_HOME": ""}}
		}},
		{"empty home", envOf("HOME", "")},
		{"tilde store follows the child home", func(e *Env, o string) Opts {
			return Opts{Env: map[string]string{"HOME": o, "SHORTCUTS_HOME": "~/.shortcuts"}}
		}},
		{"~user is looked up in the real passwd database", envOf("BIN_DIR", "~root/bin")},
		{"XDG_DATA_HOME", func(e *Env, o string) Opts { return Opts{Env: map[string]string{"XDG_DATA_HOME": o}} }},
		{"XDG_CONFIG_HOME", func(e *Env, o string) Opts { return Opts{Env: map[string]string{"XDG_CONFIG_HOME": o}} }},
		{"BIN_DIR", func(e *Env, o string) Opts { return Opts{Env: map[string]string{"BIN_DIR": o}} }},
		{"a path that only looks inside", func(e *Env, o string) Opts {
			return Opts{Env: map[string]string{"SHORTCUTS_HOME": e.Tmp + "-other"}}
		}},
		{"dot-dot out", func(e *Env, o string) Opts {
			return Opts{Env: map[string]string{"SHORTCUTS_HOME": e.Tmp + "/../" + filepath.Base(o)}}
		}},
		{"a link out", func(e *Env, o string) Opts {
			link := filepath.Join(e.Tmp, "link")
			if err := os.Symlink(o, link); err != nil {
				t.Fatal(err)
			}
			return Opts{Env: map[string]string{"SHORTCUTS_HOME": link + "/.shortcuts"}}
		}},
		{"cwd outside", func(e *Env, o string) Opts { return Opts{Cwd: o} }},
		{"Vars changed mid-test", func(e *Env, o string) Opts { e.Vars["SHORTCUTS_HOME"] = o; return Opts{} }},
		{"process env changed mid-test", func(e *Env, o string) Opts { t.Setenv("HOME", o); return Opts{} }},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			e, outside := sandboxed(t)
			assertRefused(t, e, outside, r.opts(e, outside))
		})
	}
}

// Inside the sandbox "~/elsewhere" is allowed through, still unexpanded: the child expands it.
func TestFixtures_TildeInsideTheSandboxPasses(t *testing.T) {
	e := New(t)
	useBinary(t, e, `printf %s "$SHORTCUTS_HOME" > "$HOME/seen"`+"\n")
	res := e.RunWith(Opts{Env: map[string]string{"SHORTCUTS_HOME": "~/elsewhere"}}, "new", "x", "--", "true")
	if res.Code != 0 {
		t.Fatalf("exit %d: %s", res.Code, res.Stderr)
	}
	if got, _ := os.ReadFile(filepath.Join(e.Home, "seen")); string(got) != "~/elsewhere" {
		t.Errorf("child saw SHORTCUTS_HOME=%q", got)
	}
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
	if a, b := Binary(t), Binary(t); a != b {
		t.Errorf("built twice: %s, %s", a, b)
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
