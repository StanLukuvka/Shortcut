package platform

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteJSONAtomic writes a whole file or none of it. A symlinked file is written
// where the link leads, with the mode it had. The temporary file is hidden, named after it, and removed on error.
func WriteJSONAtomic(path string, doc Doc) error {
	text, err := doc.Pretty() // before anything is created: a failed encode leaves the old file as it was
	if err != nil {
		return err
	}
	exists, err := Lexists(path)
	if err != nil {
		return err
	}
	if exists { // follow a symlinked file to where it lives; a file not there yet is created where it is named
		if path, err = Realpath(path); err != nil {
			return err
		}
	}
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
			os.Remove(tmp.Name()) // the error that got us here is the one returned
		}
	}()
	if _, err = tmp.Write(text); err != nil {
		tmp.Close() // the write error is the one returned
		return err
	}
	if haveMode {
		if err = tmp.Chmod(mode); err != nil {
			tmp.Close() // the chmod error is the one returned
			return err
		}
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close() // the sync error is the one returned
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
