package term

import (
	"bufio"
	"os"

	"github.com/StanLukuvka/Shortcut/internal/platform"
)

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
	s.Out = &Writer{s: s, f: out, tty: platform.IsTerminal(int(out.Fd()))}
	s.Err = &Writer{s: s, f: errw, tty: platform.IsTerminal(int(errw.Fd()))}
	return s
}

// Writer is one of the two output streams.
type Writer struct {
	s   *Streams
	f   *os.File
	tty bool
	err error // the first write error; after it nothing more is written
}

// IsTerminal is isatty on the stream: the dim "→ dir" line (stderr).
func (w *Writer) IsTerminal() bool { return w.tty }

// WriteString writes text to the stream through Safe, then backslashReplace. A failed write is kept for Failed.
// A byte that is not UTF-8 prints as \xNN on both streams, so no raw byte such as 0x9b (CSI on an 8-bit terminal) reaches a terminal.
func (w *Writer) WriteString(text string) { w.put(backslashReplace(Safe(text))) }

// WriteStyled writes past Safe, but not past the escape. Everything from a file must already be Safe.
func (w *Writer) WriteStyled(text string) { w.put(backslashReplace(text)) }

// Failed is the first error a write to the stream returned, or nil.
func (w *Writer) Failed() error { return w.err }

func (w *Writer) put(text string) {
	if w.err == nil {
		_, w.err = w.f.Write([]byte(text))
	}
}
