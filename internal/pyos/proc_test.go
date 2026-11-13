package pyos

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// The test binary doubles as the program under test for what only a process can show: PYOS_HELPER names the job.
func TestMain(m *testing.M) { testkit.HelperMain(m, "PYOS_HELPER", helper) }

func helper(job string) {
	switch job {
	case "cwd":
		logical, _ := os.Getwd()
		physical, _ := Getwd()
		fmt.Printf("%s\n%s\n", logical, physical)
	case "tty":
		fmt.Println(IsTerminal(0), IsTerminal(1), TerminalRows())
	case "exec": // as cli.Main does: SIGPIPE is Notify'd, then the shortcut execs
		signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
		env := append(os.Environ(), "PATH="+os.Getenv("PYOS_PATH"))
		err := ExecChild([]string{os.Getenv("PYOS_CMD"), "-c", `pwd; grep -E '^Sig(Blk|Ign)' /proc/$$/status`},
			os.Getenv("PYOS_CWD"), env, func() { fmt.Println("flushed") })
		fmt.Println("returned:", err)
		os.Exit(7)
	}
}

// With PWD naming a symlink to the cwd, os.Getwd gives the link and Getwd the physical folder.
func TestGetwd_IsPhysical(t *testing.T) {
	e := testkit.New(t)
	real := e.Folder("real")
	e.Symlink(real, filepath.Join(e.Tmp, "link"))
	res := e.RunCmd(testkit.Opts{Cwd: filepath.Join(e.Tmp, "link"), Env: map[string]string{"PYOS_HELPER": "cwd", "PWD": filepath.Join(e.Tmp, "link")}}, testkit.SelfArgs()...)
	if want := filepath.Join(e.Tmp, "link") + "\n" + real + "\n"; res.Stdout != want {
		t.Errorf("stdout %q, want %q (os.Getwd then pyos.Getwd)", res.Stdout, want)
	}
}

func TestTTY_StdinStdoutAndRows(t *testing.T) {
	e := testkit.New(t)
	opts := func(env map[string]string) testkit.Opts {
		env["PYOS_HELPER"] = "tty"
		return testkit.Opts{Env: env}
	}
	if res := e.RunCmd(opts(map[string]string{}), testkit.SelfArgs()...); res.Stdout != "false false 24\n" { // /dev/null is not a terminal
		t.Errorf("no terminal: %q", res.Stdout)
	}
	if res := e.RunCmd(opts(map[string]string{"LINES": "7"}), testkit.SelfArgs()...); res.Stdout != "false false 7\n" {
		t.Errorf("LINES: %q", res.Stdout)
	}
	if res := e.RunCmd(opts(map[string]string{"LINES": "0"}), testkit.SelfArgs()...); res.Stdout != "false false 24\n" {
		t.Errorf("LINES=0: %q", res.Stdout)
	}
	code, out := e.InTerminalCmd(opts(map[string]string{}), nil, testkit.SelfArgs()...)
	if code != 0 || out != "true true 30\r\n" { // testkit's pty is 30 rows
		t.Errorf("terminal: %d %q", code, out)
	}
}

func TestExec_FailureBeforeExecReturnsAnError(t *testing.T) {
	e := testkit.New(t)
	flushed := 0
	cwd, _ := Getwd() // chdir to where we are: the test process's cwd does not move
	if err := ExecChild([]string{"x"}, e.Tmp+"/missing", nil, func() { flushed++ }); err == nil || flushed != 1 {
		t.Errorf("a missing cwd: err = %v, flushed = %d", err, flushed)
	}
	if err := ExecChild([]string{"no-such-command-zz9"}, cwd, []string{"PATH=" + e.Tmp}, func() {}); !strings.Contains(fmt.Sprint(err), "no-such-command-zz9") {
		t.Errorf("not found: %v", err)
	}
}

func TestExec_LookPath(t *testing.T) {
	e := testkit.New(t)
	bin, other := e.Folder("bin"), e.Folder("other")
	e.WriteFile(bin+"/tool", "#!/bin/sh\n", 0o755)
	e.WriteFile(bin+"/plain", "x", 0o644)
	e.WriteFile(other+"/plain", "#!/bin/sh\n", 0o755)
	e.WriteFile(other+"/tool", "#!/bin/sh\n", 0o755)
	e.WriteFile(filepath.Join(e.Tmp, "here"), "#!/bin/sh\n", 0o755)
	if err := os.Mkdir(bin+"/adir", 0o755); err != nil {
		t.Fatal(err)
	}
	path := bin + ":" + other
	t.Chdir(e.Tmp) // an empty or "." entry would mean the cwd; it no longer matches
	for _, c := range []struct{ file, path, want string }{
		{"tool", path, bin + "/tool"},
		{"plain", path, other + "/plain"}, // a non-executable file earlier in PATH is passed over
		{"adir", bin, ""},                 // a folder is not a program
		{"none", path, ""},
		{"here", "", ""}, // PATH= : one empty entry, no longer the cwd
		{"here", ".", ""},
		{"here", bin + "::" + other, ""},
		{"here", bin + ":.:" + other, ""},
	} {
		got, err := lookPathIn(c.path, c.file)
		if (err != nil) != (c.want == "") || got != c.want {
			t.Errorf("lookPathIn(%q, %q) = %q, %v; want %q", c.path, c.file, got, err, c.want)
		}
		if err != nil && !errors.Is(err, syscall.ENOENT) {
			t.Errorf("lookPathIn(%q, %q): %v, want ENOENT", c.path, c.file, err)
		}
	}
	if _, err := lookPathIn(bin, "plain"); !errors.Is(err, syscall.EACCES) {
		t.Errorf("only a non-executable match: %v", err)
	}
	if _, err := lookPathIn(bin, "none"); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("no match: %v", err)
	}
	old := os.Getenv("PATH")
	lookPathIn(bin, "tool")
	if os.Getenv("PATH") != old {
		t.Errorf("PATH not restored: %q", os.Getenv("PATH"))
	}
	if pathOf(nil) != "/bin:/usr/bin" {
		t.Errorf("pathOf(nil) = %q", pathOf(nil))
	}
	if pathOf([]string{"A=1", "PATH=/x", "PATH=/y"}) != "/y" || pathOf([]string{"PATH="}) != "" {
		t.Error("pathOf")
	}
}

// The child's own PATH is searched, not this process's; an empty PATH in it does not reach the cwd.
func TestExec_ChildPathOverrides(t *testing.T) {
	e := testkit.New(t)
	bin := e.Folder("bin2")
	e.WriteFile(bin+"/tool", "#!/bin/sh\n", 0o755)
	e.WriteFile(filepath.Join(e.Tmp, "here"), "#!/bin/sh\n", 0o755)
	t.Chdir(e.Tmp)
	if _, err := lookPathIn(os.Getenv("PATH"), "sh"); err != nil {
		t.Skip("no sh on the process PATH")
	}
	if err := ExecChild([]string{"sh"}, e.Tmp, []string{"PATH=/nonexistent"}, func() {}); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("override ignored: %v", err)
	}
	if err := ExecChild([]string{"here"}, e.Tmp, []string{"PATH="}, func() {}); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("empty PATH found the cwd: %v", err)
	}
	if p, err := lookPathIn(pathOf([]string{"PATH=" + bin}), "tool"); err != nil || p != bin+"/tool" {
		t.Errorf("child PATH: %q %v", p, err)
	}
}

// What a shortcut inherits: flush ran, cwd changed, SIGPIPE and SIGXFSZ default (bits 13 and 25 clear in SigIgn),
// nothing blocked.
func TestExec_TheChildStartsClean(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	e := testkit.New(t)
	dir := e.Folder("run")
	res := e.RunCmd(testkit.Opts{Env: map[string]string{"PYOS_HELPER": "exec", "PYOS_CMD": "sh", "PYOS_PATH": "/bin:/usr/bin", "PYOS_CWD": dir}}, testkit.SelfArgs()...)
	if res.Code != 0 {
		t.Fatalf("exit %d, stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	if len(lines) != 4 || lines[0] != "flushed" || lines[1] != dir {
		t.Fatalf("stdout %q", res.Stdout)
	}
	ign, blk := hexOf(t, lines[2]), hexOf(t, lines[3])
	if ign&(1<<(syscall.SIGPIPE-1)) != 0 || ign&(1<<(syscall.SIGXFSZ-1)) != 0 || blk != 0 {
		t.Errorf("%s / %s", lines[2], lines[3])
	}
}

func hexOf(t *testing.T, line string) uint64 {
	var name string
	var v uint64
	if _, err := fmt.Sscanf(line, "%s %x", &name, &v); err != nil {
		t.Fatalf("%q: %v", line, err)
	}
	return v
}
