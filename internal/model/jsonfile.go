package model

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
)

// ReadJSONFile reads and decodes a JSON file. A decode error is not an *fs.PathError (see isJSONError); callers
// add the file's name. Text that is not valid UTF-8 is refused (pyjson.ErrNotUTF8).
func ReadJSONFile(path string) (pyjson.Value, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return pyjson.Decode(raw)
}

// osErrText is str(OSError): "[Errno 13] Permission denied: 'path'".
// lazy: Python's repr() of the path (quote choice, escapes) is not reproduced; the text is the plain path in single quotes.
func osErrText(err error) string {
	var pe *fs.PathError
	var en syscall.Errno
	if errors.As(err, &pe) && errors.As(pe.Err, &en) {
		msg := en.Error()
		return fmt.Sprintf("[Errno %d] %s%s: '%s'", int(en), strings.ToUpper(msg[:1]), msg[1:], pe.Path)
	}
	return err.Error()
}

// WriteJSONAtomic writes a whole file or none of it. A symlinked file is written
// where the link leads, with the mode it had. The temporary file is hidden, named after it, and removed on error.
func WriteJSONAtomic(path string, v pyjson.Value) error {
	path = pyos.Realpath(path) // follow a symlinked file to where it lives
	dir, base := filepath.Split(path)
	dir = filepath.Clean(dir)
	var mode os.FileMode
	haveMode := false
	if st, err := os.Stat(path); err == nil {
		mode, haveMode = st.Mode().Perm(), true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+base+".*.tmp") // 0600, hidden, named after the file
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmp.Name())
		}
	}()
	text := pyjson.Dumps(v, pyjson.Opts{Indent: 2}) + "\n"
	if _, err = tmp.WriteString(text); err != nil {
		tmp.Close()
		return err
	}
	if haveMode {
		if err = tmp.Chmod(mode); err != nil {
			tmp.Close()
			return err
		}
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	ok = true
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync() // the rename survives a crash only once the folder is on disk
}
