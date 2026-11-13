package textio

import (
	"bufio"
	"errors"
	"os"
	"syscall"

	"github.com/StanLukuvka/Shortcut/internal/pyos"
)

// bufSize is the buffer size: stdout is buffered this much when it is not a terminal.
const bufSize = 8192

// exit is a variable so a test can see Abort141 without ending the test binary.
var exit = os.Exit

// Streams is shortcut's stdin, stdout and stderr. Out and Err run every Write through
// Safe and backslashReplace; WriteStyled skips Safe, for our own colour codes. A closed fd 0/1/2 needs no case: since Go 1.22 the
// runtime reopens it on /dev/null before main, so writes succeed and are discarded.
type Streams struct {
	In       *os.File
	Out, Err *Writer
	in       *bufio.Reader // lazily; shared by every Ask so no input is lost between questions
}

// NewStreams wraps the three files, usually os.Stdin, os.Stdout, os.Stderr.
func NewStreams(in, out, errw *os.File) *Streams {
	s := &Streams{In: in}
	s.Out = &Writer{s: s, f: out, tty: pyos.IsTerminal(int(out.Fd()))}
	s.Out.direct = s.Out.tty // a terminal is written through (not buffered); a pipe or file is buffered
	s.Err = &Writer{s: s, f: errw, tty: pyos.IsTerminal(int(errw.Fd())), direct: true}
	return s
}

// Flush writes what stdout holds. An EPIPE comes back as the error for Main to turn into 141.
func (s *Streams) Flush() error { return s.Out.Flush() }

// Abort141 ends the process as a reader that stopped (shortcut list | head) ends one: exit 141, nothing more
// written, nothing flushed.
func (s *Streams) Abort141() { exit(141) }

// Writer is one of the two output streams.
type Writer struct {
	s      *Streams
	f      *os.File
	tty    bool
	direct bool // written straight through, not buffered
	buf    []byte
}

// File is the underlying file, for a child that inherits it.
func (w *Writer) File() *os.File { return w.f }

// IsTerminal is isatty on the stream: the diff colour and pager (stdout), the dim "→ dir" line (stderr).
func (w *Writer) IsTerminal() bool { return w.tty }

// WriteString writes text to the stream through Safe, then backslashReplace, and returns len(text). A write that fails
// with EPIPE (a reader that has gone) ends the process with 141; any other error is returned.
// A byte that is not UTF-8 prints as \xNN on both streams, so no raw byte such as 0x9b (CSI on an 8-bit terminal) reaches a terminal.
func (w *Writer) WriteString(text string) (int, error) {
	return len(text), w.put(backslashReplace(Safe(text)))
}

// WriteStyled writes past Safe, but not past the escape. Everything from a file must already be Safe.
func (w *Writer) WriteStyled(text string) error { return w.put(backslashReplace(text)) }

func (w *Writer) put(text string) error {
	if w.direct {
		return w.fail(w.write([]byte(text)))
	}
	w.buf = append(w.buf, text...)
	if len(w.buf) >= bufSize {
		return w.flushOrAbort()
	}
	return nil
}

func (w *Writer) write(p []byte) error {
	_, err := w.f.Write(p)
	return err
}

// fail ends the process on EPIPE; any other error is the caller's to report.
func (w *Writer) fail(err error) error {
	if errors.Is(err, syscall.EPIPE) {
		w.s.Abort141()
	}
	return err
}

// flushOrAbort is Flush where a flush onto a closed pipe fails (before a pager, a prompt, after a
// diff): an EPIPE ends the process with 141, as a failed buffered write does. Main's own last Flush stays the plain one.
func (w *Writer) flushOrAbort() error { return w.fail(w.Flush()) }

// Flush writes the buffer. The buffer is dropped either way: a failed write is not retried.
func (w *Writer) Flush() error {
	if len(w.buf) == 0 {
		return nil
	}
	p := w.buf
	w.buf = nil
	return w.write(p)
}
