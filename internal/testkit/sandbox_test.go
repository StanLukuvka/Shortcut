package testkit

// Ports of tests/test_fixtures.py. Nothing here points at the real home: the stand-in for it is a second temp dir,
// so if the guard ever breaks these tests fail without reaching ~/.shortcuts. cmd/shortcut is an empty main until the
// port lands, so the tests that Python ran against the real CLI run an `sh` stand-in (useBinary) and assert what
// testkit did (what it launched, where, with what env); the end-to-end halves (`r go` printing "ran") belong to the
// command tests.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { Main(m) }

// useBinary makes `sh` script body the "binary" for the rest of the test.
func useBinary(t *testing.T, e *Env, body string) {
	t.Helper()
	testBinary = e.Script("fake-shortcut", "#!/bin/sh\n"+body)
	t.Cleanup(func() { testBinary = "" })
}

// sandboxed is an Env whose refusals panic with the error (see Env.fail) instead of ending the test, with a
// stand-in binary, and a second temp dir for the real home to be.
func sandboxed(t *testing.T) (e *Env, outside string) {
	t.Helper()
	e = New(t)
	e.fail = func(err error) { panic(err) }
	useBinary(t, e, "exit 0\n")
	o, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return e, o
}

// refused runs f and reports whether it was stopped by a SandboxEscape.
func refused(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		err, _ := r.(error)
		var se SandboxEscape
		if !errors.As(err, &se) {
			t.Errorf("%s: want a SandboxEscape, got %v", what, r)
		}
	}()
	f()
}

func entries(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	l, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

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

func TestFixtures_FixtureFileOutsideIsRefused(t *testing.T) {
	e, outside := sandboxed(t)
	refused(t, "WriteStore", func() { e.WriteStore(outside+"/.shortcuts", map[string]any{}) })
	refused(t, "Symlink", func() { e.Symlink(e.Tmp, outside+"/l") })
	if n := len(entries(t, outside)); n != 0 {
		t.Errorf("outside has %d entries", n)
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

// decoded is v as a JSON round trip makes it, to compare with what a file holds.
func decoded(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFixtures_ProjectWritesWhatItIsGiven(t *testing.T) {
	e := New(t)
	imports := map[string]any{"a": "a.shortcut"}
	proj := e.Project(GO, Dirs("src"), Files(map[string]any{"bin/tool": []byte{0xff}}), Extra(map[string]any{"imports": imports}))
	if fi, err := os.Stat(filepath.Join(proj, "src")); err != nil || !fi.IsDir() {
		t.Errorf("src: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(proj, "bin", "tool")); string(b) != "\xff" {
		t.Errorf("bin/tool = %q", b)
	}
	raw, err := os.ReadFile(filepath.Join(proj, ".shortcuts"))
	if err != nil {
		t.Fatal(err)
	}
	var got any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := decoded(t, map[string]any{"version": 2, "shortcuts": GO, "imports": imports})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("store = %v, want %v", got, want)
	}
}

func TestFixtures_ProjectWithoutShortcutsHasNoStore(t *testing.T) {
	e := New(t)
	if _, err := os.Stat(filepath.Join(e.Project(nil), ".shortcuts")); err == nil {
		t.Error("a store was written")
	}
}

func TestFixtures_ApprovedProjectRunsAtOnce(t *testing.T) {
	e := New(t)
	useBinary(t, e, `pwd -P > "$HOME/approve-cwd"; echo "$@" > "$HOME/approve-args"`+"\n")
	proj := e.Project(GO, Approved()) // the real `r go` check
	if got, _ := os.ReadFile(filepath.Join(e.Home, "approve-args")); string(got) != "update -y\n" {
		t.Errorf("approve ran %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(e.Home, "approve-cwd")); strings.TrimSpace(string(got)) != proj {
		t.Errorf("approve ran in %q, want %q", got, proj)
	}
}

func TestFixtures_BrokenProjectReadsAsCorrupt(t *testing.T) {
	e := New(t) // the "corrupt registry" message itself
	if b, _ := os.ReadFile(filepath.Join(e.BrokenProject(), ".shortcuts")); string(b) != brokenJSON {
		t.Errorf("store = %q", b)
	}
}

func TestFixtures_CollectionImports(t *testing.T) {
	e := New(t) // the `init --global` and `r hi` halves
	src := e.Collection(HI, Files(map[string]any{"bin/a.sh": "echo a\n"}), Mode(0o755))
	if fi, err := os.Stat(filepath.Join(src, "bin", "a.sh")); err != nil || fi.Mode()&0o111 == 0 {
		t.Errorf("bin/a.sh not executable: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(src, ".shortcuts"))
	want := `{"shortcuts":{"hi":{"export":true,"inline":"echo hi"}},"version":2}`
	if strings.TrimSpace(string(raw)) != want {
		t.Errorf("store = %s", raw)
	}
}

func TestFixtures_SharedFixturesAreLeftAsTheyWere(t *testing.T) {
	e := New(t)
	useBinary(t, e, "exit 0\n")
	before := fmt.Sprint(GO, HI)
	e.Project(GO, Approved())
	e.Collection(HI)
	if after := fmt.Sprint(GO, HI); after != before {
		t.Errorf("fixtures changed: %s -> %s", before, after)
	}
}
