// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (T-0). Don't hand-edit without checking the plan.

package testkit

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Key is one step of a terminal session: wait until Wait appears in the output, then do one of the rest.
type Key struct {
	Wait   string    // wait until this appears in the output ...
	Send   []byte    // ... then type these bytes,
	Signal os.Signal // or send this signal to the child,
	Do     func()    // or call this
}

// InTerminal runs the binary on a fresh 30x100 pseudo-terminal, as if typed into a real one (= run_in_terminal):
// its own session, the terminal as controlling tty, 15s. The output has the terminal's \r\n.
func (e *Env) InTerminal(o Opts, keys []Key, args ...string) (code int, out string) {
	e.T.Helper()
	return e.terminal(o, "", keys, append([]string{Binary(e.T)}, args...))
}

func (e *Env) InTerminalCmd(o Opts, keys []Key, argv ...string) (int, string) {
	e.T.Helper()
	return e.terminal(o, "", keys, argv)
}

// InTerminalStdoutTo is InTerminal with stdout sent to file, as `> file` would; the terminal keeps stdin and stderr.
func (e *Env) InTerminalStdoutTo(o Opts, file string, keys []Key, args ...string) (int, string) {
	e.T.Helper()
	if err := refuseUnlessInside(e.Tmp, "the stdout file", file); err != nil {
		e.die(err)
	}
	return e.terminal(o, file, keys, append([]string{Binary(e.T)}, args...))
}

func (e *Env) terminal(o Opts, stdoutFile string, keys []Key, argv []string) (int, string) {
	e.T.Helper()
	cwd, env := e.launch(o)
	d := e.timeout(o, 15*time.Second)
	master, slave, err := openPty()
	if err != nil {
		e.die(err)
	}
	defer master.Close()
	if err := setWinsize(master, 30, 100); err != nil {
		e.die(err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = cwd, env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	var file *os.File
	if stdoutFile != "" {
		if file, err = os.Create(stdoutFile); err != nil {
			e.die(err)
		}
		cmd.Stdout = file
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	err = cmd.Start()
	slave.Close() // or the master never sees the child's end as EIO
	if file != nil {
		file.Close()
	}
	if err != nil {
		e.die(err)
	}
	quit := make(chan struct{})
	defer close(quit)
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	chunks := make(chan []byte)
	go func() {
		defer close(chunks)
		buf := make([]byte, 65536)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				select {
				case chunks <- append([]byte(nil), buf[:n]...):
				case <-quit:
					return
				}
			}
			if err != nil { // EIO: the slave is closed
				return
			}
		}
	}()

	pending := keys
	var out []byte
	deadline := time.Now().Add(d)
loop:
	for time.Now().Before(deadline) {
		if len(pending) > 0 && bytes.Contains(out, []byte(pending[0].Wait)) {
			k := pending[0]
			pending = pending[1:]
			switch {
			case k.Send != nil:
				master.Write(k.Send)
			case k.Do != nil:
				k.Do()
			case k.Signal != nil:
				cmd.Process.Signal(k.Signal)
			}
		}
		select {
		case c, ok := <-chunks:
			if !ok {
				break loop
			}
			out = append(out, c...)
		case <-time.After(100 * time.Millisecond):
			select {
			case <-exited:
				break loop
			default:
			}
		}
	}
	select {
	case <-exited:
	case <-time.After(time.Until(deadline) + d):
		cmd.Process.Kill()
		e.dief("%q still running after %v on a terminal; output so far:\n%s", argv, d, out)
	}
	return codeOf(cmd.ProcessState), strings.ToValidUTF8(string(out), "�")
}

// setWinsize is TIOCSWINSZ with struct{rows, cols, x, y uint16}.
func setWinsize(f *os.File, rows, cols uint16) error {
	ws := [4]uint16{rows, cols, 0, 0}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws))); e != 0 {
		return e
	}
	return nil
}
