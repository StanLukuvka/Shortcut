package textio

import (
	"bufio"
	"errors"
	"os"
	"syscall"

	"github.com/StanLukuvka/Shortcut/internal/pyos"
)

// bufSize is Python's io.DEFAULT_BUFFER_SIZE: stdout is buffered this much when it is not a terminal.
const bufSize = 8192

// exit is a variable so a test can see Abort141 without ending the test binary.
var exit = os.Exit

// Streams is shortcut's stdin, stdout and stderr (= SafeOutput over sys.std*). Out and Err run every Write through
// Safe; WriteStyled is the way past it, for our own colour codes. A closed fd 0/1/2 needs no case: since Go 1.22 the
// runtime reopens it on /dev/null before main, so writes succeed and are discarded (C22).
type Streams struct {
	In       *os.File
	Out, Err *Writer
	in       *bufio.Reader // lazily; shared by every Ask so no input is lost between questions
}

// NewStreams wraps the three files, usually os.Stdin, os.Stdout, os.Stderr.
func NewStreams(in, out, errw *os.File) *Streams {
	s := &Streams{In: in}
	s.Out = &Writer{s: s, f: out, tty: pyos.IsTerminal(int(out.Fd()))}
	s.Out.direct = s.Out.tty // a terminal is written through (Python line-buffers it); a pipe or file is buffered
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

// Write is io.Writer through Safe. A reader that has gone ends the process with 141.
func (w *Writer) Write(p []byte) (int, error) { return len(p), w.put(Safe(string(p))) }

func (w *Writer) WriteString(text string) (int, error) { return len(text), w.put(Safe(text)) }

// WriteStyled is write_styled: past Safe. Everything from a file must already be Safe.
func (w *Writer) WriteStyled(text string) error { return w.put(text) }

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

// flushOrAbort is Flush where Python's sys.stdout.flush() raises BrokenPipeError (before a pager, a prompt, after a
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
