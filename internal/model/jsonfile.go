package model

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/StanLukuvka/Shortcut/internal/ordjson"
	"github.com/StanLukuvka/Shortcut/internal/osutil"
)

// ReadJSONFile reads and decodes a JSON file. A decode error is not an *fs.PathError (see isJSONError); callers
// add the file's name. Text that is not valid UTF-8 is refused (ordjson.ErrNotUTF8).
func ReadJSONFile(path string) (ordjson.Value, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ordjson.Decode(raw)
}

// WriteJSONAtomic writes a whole file or none of it. A symlinked file is written
// where the link leads, with the mode it had. The temporary file is hidden, named after it, and removed on error.
func WriteJSONAtomic(path string, v ordjson.Value) error {
	text, err := ordjson.MarshalIndent(v) // before anything is created: a refused write leaves the old file as it was
	if err != nil {
		return err
	}
	path = osutil.Realpath(path) // follow a symlinked file to where it lives
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
	if _, err = tmp.Write(append(text, '\n')); err != nil {
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
