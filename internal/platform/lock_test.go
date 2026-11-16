package platform

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestLock_ASymlinkedLockIsRefused(t *testing.T) {
	e := testkit.New(t)
	victim := e.WriteFile(filepath.Join(e.Tmp, "victim"), "keep\n", 0)
	e.Symlink(victim, filepath.Join(e.Tmp, "s/.lock"))
	ran := false
	err := WithLock(filepath.Join(e.Tmp, "s/.lock"), func() error { ran = true; return nil })
	if !errors.Is(err, syscall.ELOOP) || ran {
		t.Errorf("err = %v, ran = %v; want ELOOP and fn not run", err, ran)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep\n" {
		t.Errorf("victim is %q", b)
	}
}

func TestLock_MakesFoldersRunsFnAndExcludes(t *testing.T) {
	e := testkit.New(t)
	path := filepath.Join(e.Tmp, "locks/deep/x.lock")
	boom := errors.New("boom")
	if err := WithLock(path, func() error { return boom }); err != boom {
		t.Fatalf("fn's error must come back, got %v", err)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o644&^umask() {
		t.Errorf("lock file: %v %v", st, err)
	}
	holding, release := make(chan struct{}), make(chan struct{})
	done := make(chan error)
	go func() {
		done <- WithLock(path, func() error { close(holding); <-release; return nil })
	}()
	<-holding
	var second atomic.Bool
	go func() { done <- WithLock(path, func() error { second.Store(true); return nil }) }()
	time.Sleep(100 * time.Millisecond)
	if second.Load() {
		t.Fatal("a second holder got in while the first held the lock")
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if !second.Load() {
		t.Error("the second holder never ran")
	}
}

func umask() os.FileMode {
	old := syscall.Umask(0)
	syscall.Umask(old)
	return os.FileMode(old)
}
