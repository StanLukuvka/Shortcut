package pyos

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestWalk_LinksAreListedAsDirsButNotEntered(t *testing.T) {
	e := testkit.New(t)
	top := filepath.Join(e.Tmp, "top")
	e.WriteFile(top+"/a/f1", "1", 0)
	e.WriteFile(top+"/a/b/f2", "2", 0)
	e.Symlink("a", top+"/dl")           // link to a folder: in dirs, not entered
	e.Symlink("a/f1", top+"/fl")        // link to a file: in files
	e.Symlink("nowhere", top+"/broken") // broken link: in files
	var seen []string
	Walk(top, func(dir string, dirs, files []string) {
		seen = append(seen, fmt.Sprintf("%s %v %v", dir[len(top):], dirs, files))
	})
	want := []string{" [a dl] [broken fl]", "/a [b] [f1]", "/a/b [] [f2]"}
	if !slices.Equal(seen, want) {
		t.Errorf("got %q, want %q", seen, want)
	}
}

func TestWalk_AFolderThatCannotBeReadIsSkipped(t *testing.T) {
	e := testkit.New(t)
	top := filepath.Join(e.Tmp, "top")
	e.WriteFile(top+"/x/f", "1", 0)
	e.WriteFile(top+"/y/f", "1", 0)
	if err := os.Chmod(top+"/x", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadDir(top + "/x"); err == nil {
		t.Skip("permissions are not enforced here (root)")
	}
	var visited []string
	Walk(top, func(dir string, dirs, files []string) { visited = append(visited, dir[len(top):]) })
	if want := []string{"", "/y"}; !slices.Equal(visited, want) {
		t.Errorf("visited %q, want %q", visited, want)
	}
	Walk(top+"/missing", func(string, []string, []string) { t.Error("visited a missing folder") })
}

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
