// Package terminal is where shortcut's text leaves and enters: the streams, the questions it asks and the exit error.
// A command's words are described by Command and read by its parser, argparse.
package terminal

import (
	"os"

	"github.com/StanLukuvka/Shortcut/internal/platform"
)

// Streams is shortcut's stdin, stdout and stderr. A closed fd 0/1/2 needs no case: since Go 1.22 the
// runtime reopens it on /dev/null before main, so writes succeed and are discarded.
type Streams struct {
	In       *os.File
	Out, Err *Writer
}

// NewStreams wraps the three files, usually os.Stdin, os.Stdout, os.Stderr.
func NewStreams(in, out, errw *os.File) *Streams {
	s := &Streams{In: in}
	s.Out = &Writer{f: out, tty: platform.IsTerminal(int(out.Fd()))}
	s.Err = &Writer{f: errw, tty: platform.IsTerminal(int(errw.Fd()))}
	return s
}

// Writer is one of the two output streams.
type Writer struct {
	f   *os.File
	tty bool
	err error // the first write error; after it nothing more is written
}

// IsTerminal is isatty on the stream: the dim "→ dir" line (stderr).
func (w *Writer) IsTerminal() bool { return w.tty }

// WriteString writes text to the stream as it is. A failed write is kept for Failed.
func (w *Writer) WriteString(text string) {
	if w.err == nil {
		_, w.err = w.f.WriteString(text)
	}
}

// Failed is the first error a write to the stream returned, or nil.
func (w *Writer) Failed() error { return w.err }
