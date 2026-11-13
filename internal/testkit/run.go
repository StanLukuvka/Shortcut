package testkit

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Opts for starting a process.
type Opts struct {
	Cwd      string            // default Home
	Env      map[string]string // overrides on top of Env.Vars
	Input    *string           // nil = /dev/null as stdin
	Timeout  time.Duration     // default 60s (15s on a terminal)
	CloseFDs []int             // of 0, 1, 2: RunCmd starts the program with these closed, as `sh -c 'cmd >&-'` does
}

// Result of a finished process.
type Result struct {
	Code           int // a signal death is -signal, as Python's returncode
	Stdout, Stderr string
}

// launch is where every child starts (= launch_args): the cwd and env for it, after refusing any that leave the
// sandbox. The process's own env is checked too, as in-process code reads it. PWD is the cwd unless Opts.Env says
// otherwise (the symlinked-cwd tests set it to the link).
func (e *Env) launch(o Opts) (cwd string, env []string) {
	e.T.Helper()
	cwd = o.Cwd
	if cwd == "" {
		cwd = e.Home
	}
	m := maps.Clone(e.Vars)
	maps.Copy(m, o.Env)
	if _, ok := o.Env["PWD"]; !ok {
		m["PWD"] = cwd
	}
	proc := map[string]string{"HOME": os.Getenv("HOME")}
	for _, s := range sandboxedPaths {
		proc[s.Name] = os.Getenv(s.Name)
	}
	for _, checked := range []map[string]string{m, proc} {
		if err := RefuseUnlessSandboxed(e.Tmp, checked, cwd); err != nil {
			e.die(err)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		env = append(env, k+"="+m[k])
	}
	return cwd, env
}

func codeOf(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return -int(ws.Signal())
	}
	return ps.ExitCode()
}

func (e *Env) timeout(o Opts, def time.Duration) time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return def
}

// Run starts the built binary (= run_cli).
func (e *Env) Run(args ...string) Result { e.T.Helper(); return e.RunWith(Opts{}, args...) }

func (e *Env) RunWith(o Opts, args ...string) Result {
	e.T.Helper()
	return e.RunCmd(o, append([]string{Binary(e.T)}, args...)...)
}

// RunCmd starts any program: bash, git, sh, tar (= run_cmd).
func (e *Env) RunCmd(o Opts, argv ...string) Result {
	e.T.Helper()
	cwd, env := e.launch(o)
	if len(o.CloseFDs) > 0 {
		// lazy: sh closes them and execs; os/exec can only hand a child /dev/null or a pipe. Upgrade: a pre-exec hook.
		redirs := ""
		for _, fd := range o.CloseFDs {
			if fd < 0 || fd > 2 {
				e.dief("CloseFDs: %d is not one of 0, 1, 2", fd)
			}
			redirs += fmt.Sprintf(" %d>&-", fd) // >&- closes input and output alike
		}
		argv = append([]string{"sh", "-c", `exec "$@"` + redirs, "sh"}, argv...)
	}
	d := e.timeout(o, 60*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = cwd, env
	cmd.WaitDelay = 2 * time.Second
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if o.Input != nil {
		cmd.Stdin = strings.NewReader(*o.Input)
	}
	err := cmd.Run()
	if ctx.Err() != nil {
		e.dief("%q timed out after %v\nstdout: %s\nstderr: %s", argv, d, out.String(), errb.String())
	}
	if cmd.ProcessState == nil {
		e.dief("%q: %v", argv, err)
	}
	// lazy: no universal-newline translation (Python's text=True turned \r\n and \r into \n); tests see raw bytes.
	return Result{codeOf(cmd.ProcessState), out.String(), errb.String()}
}

// Shell runs a bash script with a PATH dir whose `shortcut` is the binary (replaces CLI_SHELL).
func (e *Env) Shell(o Opts, script string) Result {
	e.T.Helper()
	e.ensureToolBin()
	env := maps.Clone(o.Env)
	if env == nil {
		env = map[string]string{}
	}
	path, ok := env["PATH"]
	if !ok {
		path = e.Vars["PATH"]
	}
	if !strings.HasPrefix(path, e.ToolBin+":") {
		env["PATH"] = e.ToolBin + ":" + path
	}
	o.Env = env
	return e.RunCmd(o, "bash", "-c", script)
}

// ensureToolBin puts a `shortcut` link to the binary in ToolBin.
func (e *Env) ensureToolBin() {
	e.T.Helper()
	link := filepath.Join(e.ToolBin, "shortcut")
	if _, err := os.Lstat(link); err == nil {
		return
	}
	if err := os.MkdirAll(e.ToolBin, 0o755); err != nil {
		e.die(err)
	}
	if err := os.Symlink(Binary(e.T), link); err != nil {
		e.die(err)
	}
}

// Proc is a process left running (= popen_cli).
type Proc struct {
	e    *Env
	cmd  *exec.Cmd
	out  *bufio.Reader
	errb *bytes.Buffer
}

// Start leaves the binary running, with stdout on a pipe the test reads and stderr collected for Wait.
func (e *Env) Start(o Opts, args ...string) *Proc {
	e.T.Helper()
	cwd, env := e.launch(o)
	cmd := exec.Command(Binary(e.T), args...)
	cmd.Dir, cmd.Env = cwd, env
	p := &Proc{e: e, cmd: cmd, errb: &bytes.Buffer{}}
	cmd.Stderr = p.errb
	if o.Input != nil {
		cmd.Stdin = strings.NewReader(*o.Input)
	}
	r, w, err := os.Pipe()
	if err != nil {
		e.die(err)
	}
	cmd.Stdout = w
	err = cmd.Start()
	w.Close()
	if err != nil {
		r.Close()
		e.die(err)
	}
	p.out = bufio.NewReader(r)
	e.T.Cleanup(func() { cmd.Process.Kill(); r.Close() })
	return p
}

func (p *Proc) Stdout() *bufio.Reader { return p.out }

// Pid is the process id of the child.
func (p *Proc) Pid() int { return p.cmd.Process.Pid }

func (p *Proc) Signal(s os.Signal) {
	p.e.T.Helper()
	if err := p.cmd.Process.Signal(s); err != nil {
		p.e.die(err)
	}
}

// Wait collects what is left of stdout (what the test has not read) and stderr, and the exit code.
func (p *Proc) Wait(timeout time.Duration) Result {
	p.e.T.Helper()
	done := make(chan Result, 1)
	go func() {
		rest, _ := io.ReadAll(p.out)
		p.cmd.Wait()
		r := Result{Code: -1, Stdout: string(rest), Stderr: p.errb.String()}
		if p.cmd.ProcessState != nil {
			r.Code = codeOf(p.cmd.ProcessState)
		}
		done <- r
	}()
	select {
	case r := <-done:
		return r
	case <-time.After(timeout):
		p.cmd.Process.Kill()
		p.e.dief("process still running after %v", timeout)
		return Result{}
	}
}

// BashComplete is what `shortcut <words><tab>` offers; the last word is the one being completed.
func (e *Env) BashComplete(words ...string) []string {
	e.T.Helper()
	line := append([]string{"shortcut"}, words...)
	driver := e.Run("completion", "bash").Stdout +
		fmt.Sprintf("COMP_WORDS=(%s); COMP_CWORD=%d; _shortcut\n", shquote(line), len(line)-1) +
		"printf \"%s\\n\" \"${COMPREPLY[@]}\"\n"
	// Shell, not RunCmd: the script runs `shortcut list --names`, so the test binary must be on PATH
	// (a `shortcut` installed on the developer's machine would otherwise hide that).
	res := e.Shell(Opts{}, driver)
	if res.Stderr != "" {
		e.dief("bash completion wrote to stderr: %s", res.Stderr)
	}
	var offered []string
	for _, l := range strings.Split(res.Stdout, "\n") {
		if l != "" {
			offered = append(offered, l)
		}
	}
	return offered
}

// shquote is shlex.join.
func shquote(words []string) string {
	q := make([]string, len(words))
	for i, w := range words {
		safe := w != ""
		for _, r := range w {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("@%+=:,./-_", r)) {
				safe = false
			}
		}
		if safe {
			q[i] = w
		} else {
			q[i] = "'" + strings.ReplaceAll(w, "'", `'"'"'`) + "'"
		}
	}
	return strings.Join(q, " ")
}
