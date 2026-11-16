package commands

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestRunning_ExitCodeAndSignalsPassStraightThrough(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{
		"fail": testkit.Inline("echo out; echo err >&2; exit 42"),
		"pid":  testkit.Inline(`echo "$$"; exec sleep 30`),
		"trap": testkit.Inline(`trap 'kill $!; exit 7' USR1; echo ready; sleep 30 >/dev/null 2>&1 & wait`),
	})
	// stdout and stderr stay apart, and the exit code is the shortcut's own
	res := e.RunWith(testkit.Opts{Env: map[string]string{"SHORTCUT_SHOW_DIR": "0"}}, "r", "fail")
	if res.Code != 42 || res.Stdout != "out\n" || res.Stderr != "err\n" {
		t.Errorf("fail: %+v", res)
	}

	// The run is the shortcut's process: same pid, and nothing in between to relay a signal
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP, syscall.SIGKILL} {
		p := e.Start(testkit.Opts{}, "run", "pid")
		line, err := p.Stdout().ReadString('\n')
		if err != nil || strings.TrimSpace(line) != strconv.Itoa(p.Pid()) {
			t.Fatalf("%v: first line %q (%v), pid %d", sig, line, err, p.Pid())
		}
		p.Signal(sig)
		if res := p.Wait(5 * time.Second); res.Code != -int(sig) {
			t.Errorf("%v: exit %d", sig, res.Code)
		}
	}

	// A signal the shortcut handles is its to answer
	p := e.Start(testkit.Opts{}, "run", "trap")
	if line, _ := p.Stdout().ReadString('\n'); line != "ready\n" {
		t.Fatalf("first line %q", line)
	}
	p.Signal(syscall.SIGUSR1)
	if res := p.Wait(5 * time.Second); res.Code != 7 {
		t.Errorf("USR1: exit %d", res.Code)
	}
}

// bash pipes the run into head and and reports how the run ended (141 = SIGPIPE).
func TestRunning_AShortcutStartsWithSigpipeOn(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"seq": testkit.Inline("seq 1 1000000")})
	res := e.Shell(testkit.Opts{Timeout: 10 * time.Second}, `shortcut run seq | head -n 1; echo "${PIPESTATUS[0]}" >&2`)
	if !strings.HasPrefix(res.Stdout, "1\n") {
		t.Errorf("head: %q", res.Stdout)
	}
	if !strings.HasSuffix(res.Stderr, "141\n") || strings.Contains(res.Stderr, "Broken pipe") {
		t.Errorf("seq did not die of SIGPIPE: stderr %q", res.Stderr)
	}
}

// The signal dispositions a shortcut starts with: SIGPIPE and SIGXFSZ default, nothing blocked.
func TestRunning_ASignalMaskAndDispositionsAreTheDefaults(t *testing.T) {
	if _, err := os.Stat("/proc/self/status"); err != nil {
		t.Skip("no /proc")
	}
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"sig": testkit.Inline("grep -E '^Sig(Blk|Ign)' /proc/self/status")})
	res := e.RunWith(testkit.Opts{Env: map[string]string{"SHORTCUT_SHOW_DIR": "0"}}, "r", "sig")
	masks := map[string]uint64{}
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		name, hex, _ := strings.Cut(line, ":")
		v, err := strconv.ParseUint(strings.TrimSpace(hex), 16, 64)
		if err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		masks[name] = v
	}
	if len(masks) != 2 {
		t.Fatalf("stdout %q stderr %q", res.Stdout, res.Stderr)
	}
	if masks["SigBlk"] != 0 {
		t.Errorf("SigBlk %x, want 0", masks["SigBlk"])
	}
	for _, sig := range []syscall.Signal{syscall.SIGPIPE, syscall.SIGXFSZ} {
		if masks["SigIgn"]&(1<<(uint(sig)-1)) != 0 {
			t.Errorf("SigIgn %x has %v ignored", masks["SigIgn"], sig)
		}
	}
}

func TestRunning_AClosedStdoutIsTheShortcutsProblem(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"quiet": testkit.Inline("true")})
	res := e.Shell(testkit.Opts{}, "shortcut r quiet >&-")
	if res.Code != 0 || strings.Contains(res.Stderr, "Traceback") || strings.Contains(res.Stderr, "panic") {
		t.Errorf("exit %d stderr %q", res.Code, res.Stderr)
	}
}

// With fds 0, 1 or 2 closed the Go runtime puts /dev/null in their place, so the commands exit as they would
// with them open, print nothing anywhere visible and don't panic (a Go panic exits 2). What the child sees there is /dev/null.
func TestRunning_ClosedStdFDsDoNotStopACommand(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"go": testkit.Inline("echo ran")})
	for _, fds := range [][]int{{0}, {1}, {2}, {0, 1, 2}} {
		for _, args := range [][]string{{"list"}, {"r", "go"}, {"show", "go"}} {
			if res := e.RunCmd(testkit.Opts{CloseFDs: fds}, append([]string{testkit.Binary(t)}, args...)...); res.Code != 0 {
				t.Errorf("%v closed, %v: exit %d stdout %q stderr %q", fds, args, res.Code, res.Stdout, res.Stderr)
			}
		}
	}
	// stdout closed but stderr kept: nothing reaches stderr either
	res := e.RunCmd(testkit.Opts{CloseFDs: []int{1}}, testkit.Binary(t), "list")
	if res.Stderr != "" {
		t.Errorf("stderr %q", res.Stderr)
	}
}

// The caller's cwd is the physical one, whatever PWD says: SHORTCUT_CALLER_CWD, show --json's run_cwd and the
// project a run belongs to (the approval is recorded against the real path).
func TestCwd_ASymlinkedCwdIsThePhysicalOne(t *testing.T) {
	e := testkit.New(t)
	real := e.Folder("real")
	link := filepath.Join(e.Tmp, "link")
	e.Symlink(real, link)
	e.WriteStore(e.Store, map[string]any{"where": testkit.Inline(`echo "$SHORTCUT_CALLER_CWD"; pwd -P`), "c": testkit.Inline("true")})
	res := e.RunWith(testkit.Opts{Cwd: link}, "r", "where")
	if res.Code != 0 || res.Stdout != real+"\n"+real+"\n" || !strings.HasSuffix(res.Stderr, "→ "+real+"\n") {
		t.Errorf("from the link: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	info := decodeObject(t, e.RunWith(testkit.Opts{Cwd: link}, "show", "--json", "c").Stdout)
	testkit.Eq(t, "run_cwd", info["run_cwd"], real)

	proj := e.Project(testkit.GO, testkit.Approved())
	plink := filepath.Join(e.Tmp, "plink")
	e.Symlink(proj, plink)
	res = e.RunWith(testkit.Opts{Cwd: plink}, "r", "go")
	if res.Code != 0 || res.Stdout != "ran\n" {
		t.Errorf("project through a link: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
}
