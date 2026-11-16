package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// The test binary doubles as the program under test for what only a process can show: PLATFORM_HELPER names the job.
func TestMain(m *testing.M) { testkit.HelperMain(m, "PLATFORM_HELPER", helper) }

func helper(job string) {
	switch job {
	case "cwd":
		logical, _ := os.Getwd()
		physical, _ := Getwd()
		fmt.Printf("%s\n%s\n", logical, physical)
	case "tty":
		fmt.Println(IsTerminal(0), IsTerminal(1))
	case "bash":
		fmt.Println("returned:", ExecChild([]string{"bash"}, ".", nil))
		os.Exit(7)
	case "exec":
		env := append(os.Environ(), "PATH="+os.Getenv("PLATFORM_PATH"))
		err := ExecChild([]string{os.Getenv("PLATFORM_CMD"), "-c", `pwd; exec grep -E '^Sig(Blk|Ign)' /proc/self/status`},
			os.Getenv("PLATFORM_CWD"), env)
		fmt.Println("returned:", err)
		os.Exit(7)
	}
}

// With PWD naming a symlink to the cwd, os.Getwd gives the link and Getwd the physical folder.
func TestGetwd_IsPhysical(t *testing.T) {
	e := testkit.New(t)
	real := e.Folder("real")
	e.Symlink(real, filepath.Join(e.Tmp, "link"))
	res := e.RunCmd(testkit.Opts{Cwd: filepath.Join(e.Tmp, "link"), Env: map[string]string{"PLATFORM_HELPER": "cwd", "PWD": filepath.Join(e.Tmp, "link")}}, testkit.SelfArgs()...)
	if want := filepath.Join(e.Tmp, "link") + "\n" + real + "\n"; res.Stdout != want {
		t.Errorf("stdout %q, want %q (os.Getwd then platform.Getwd)", res.Stdout, want)
	}
}

func TestTTY_StdinAndStdout(t *testing.T) {
	e := testkit.New(t)
	opts := func(env map[string]string) testkit.Opts {
		env["PLATFORM_HELPER"] = "tty"
		return testkit.Opts{Env: env}
	}
	if res := e.RunCmd(opts(map[string]string{}), testkit.SelfArgs()...); res.Stdout != "false false\n" { // /dev/null is not a terminal
		t.Errorf("no terminal: %q", res.Stdout)
	}
	code, out := e.InTerminalCmd(opts(map[string]string{}), nil, testkit.SelfArgs()...)
	if code != 0 || out != "true true\r\n" {
		t.Errorf("terminal: %d %q", code, out)
	}
}

func TestExec_FailureBeforeExecReturnsAnError(t *testing.T) {
	e := testkit.New(t)
	cwd, _ := Getwd() // chdir to where we are: the test process's cwd does not move
	if err := ExecChild([]string{"x"}, e.Tmp+"/missing", nil); err == nil {
		t.Errorf("a missing cwd: err = %v", err)
	}
	if err := ExecChild([]string{"no-such-command-zz9"}, cwd, []string{"PATH=" + e.Tmp}); !strings.Contains(fmt.Sprint(err), "no-such-command-zz9") {
		t.Errorf("not found: %v", err)
	}
}

// A bash in the cwd is never found through an empty, "." or relative PATH entry. A child process tries it, so a wrong
// exec cannot replace the test: the planted bash would exit 0 without printing "returned:".
func TestExec_CwdIsNotOnThePath(t *testing.T) {
	e := testkit.New(t)
	dir := e.Folder("here")
	e.WriteFile(dir+"/bash", "#!/bin/sh\n", 0o755)
	for _, path := range []string{"", ".", "bin", ":", "bin:."} {
		res := e.RunCmd(testkit.Opts{Cwd: dir, Env: map[string]string{"PLATFORM_HELPER": "bash", "PATH": path}}, testkit.SelfArgs()...)
		if res.Code != 7 || !strings.HasPrefix(res.Stdout, "returned: ") {
			t.Errorf("PATH=%q: exit %d, stdout %q", path, res.Code, res.Stdout)
		}
	}
}

// What a shortcut inherits: cwd changed, SIGPIPE and SIGXFSZ default (bits 13 and 25 clear in SigIgn),
// nothing blocked.
func TestExec_TheChildStartsClean(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	e := testkit.New(t)
	dir := e.Folder("run")
	res := e.RunCmd(testkit.Opts{Env: map[string]string{"PLATFORM_HELPER": "exec", "PLATFORM_CMD": "sh", "PLATFORM_PATH": "/bin:/usr/bin", "PLATFORM_CWD": dir}}, testkit.SelfArgs()...)
	if res.Code != 0 {
		t.Fatalf("exit %d, stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	if len(lines) != 3 || lines[0] != dir {
		t.Fatalf("stdout %q", res.Stdout)
	}
	blk, ign := hexOf(t, lines[1]), hexOf(t, lines[2])
	if ign&(1<<(syscall.SIGPIPE-1)) != 0 || ign&(1<<(syscall.SIGXFSZ-1)) != 0 || blk != 0 {
		t.Errorf("%s / %s", lines[1], lines[2])
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
