//go:build linux || darwin

package osutil

import (
	"os"
	"path/filepath"
	"syscall"
)

// WithLock runs fn holding an exclusive flock on path (helpers.registry_lock): folders made first, the file opened
// O_APPEND|O_CREAT|O_NOFOLLOW mode 0644 so a symlinked .lock is refused (ELOOP) and its target untouched. Not
// reentrant: never nest it. Closing the file drops the lock.
func WithLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR { // the Go runtime signals threads; a bare syscall must retry
			break
		}
	}
	if err != nil {
		return err
	}
	return fn()
}
