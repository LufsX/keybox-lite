package lockfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func open(dir *os.File, name, legacy string, cache bool, boot func() (string, error)) (*os.File, error) {
	return openContext(context.Background(), dir, name, legacy, cache, boot)
}

func layoutDir(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
func sameBoot(id string) func() (string, error) { return func() (string, error) { return id, nil } }

func TestRuntimeLockLayoutKeepsScopesIndependent(t *testing.T) {
	dir := layoutDir(t)
	action, err := open(dir, "action.lock", "action.lock", false, sameBoot("boot-a"))
	if err != nil {
		t.Fatal(err)
	}
	defer action.Close()
	if filepath.Dir(action.Name()) != filepath.Join(dir.Name(), "locks") {
		t.Fatal(action.Name())
	}
	if err := syscall.Flock(int(action.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	config, err := OpenAt(dir, "config.lock")
	if err != nil {
		t.Fatal(err)
	}
	defer config.Close()
	if err := syscall.Flock(int(config.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	second, err := OpenAt(dir, "action.lock")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := syscall.Flock(int(second.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Fatal("action lock bypassed")
	}
	config.Close()
	if err := syscall.Flock(int(second.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Fatal("closing another scope released action lock")
	}
	initializing := layoutDir(t)
	layout, err := child(initializing, "locks", true)
	if err != nil {
		t.Fatal(err)
	}
	defer layout.Close()
	if err := syscall.Flock(int(layout.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if f, err := openContext(ctx, initializing, "action.lock", "action.lock", false, sameBoot("boot-a")); !errors.Is(err, context.DeadlineExceeded) {
		if f != nil {
			f.Close()
		}
		t.Fatal("layout wait ignored cancellation", err)
	}
}

func TestLegacyLockMigrationRequiresAnotherBoot(t *testing.T) {
	dir := layoutDir(t)
	old, err := regular(dir, "action.lock", true)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := old.Stat()
	if err := syscall.Flock(int(old.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	pending, err := open(dir, "action.lock", "action.lock", false, sameBoot("boot-a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(pending.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Fatal("legacy holder bypassed")
	}
	pending.Close()
	old.Close()
	moved, err := open(dir, "action.lock", "action.lock", false, sameBoot("boot-b"))
	if err != nil {
		t.Fatal(err)
	}
	defer moved.Close()
	after, _ := moved.Stat()
	if !os.SameFile(before, after) {
		t.Fatal("migration changed the lock inode")
	}
	if _, err := os.Stat(filepath.Join(dir.Name(), "action.lock")); !os.IsNotExist(err) {
		t.Fatal("legacy lock remained")
	}

	cache := layoutDir(t)
	key := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	legacy, err := regular(cache, key+".lock", true)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	f, err := open(cache, "status.lock", key+".lock", true, sameBoot("boot-a"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	f, err = open(cache, "status.lock", key+".lock", true, sameBoot("boot-b"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if filepath.Base(f.Name()) != "status.lock" {
		t.Fatal(f.Name())
	}
	other, err := OpenCache(cache, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	one, _ := f.Stat()
	two, _ := other.Stat()
	if !os.SameFile(one, two) {
		t.Fatal("route change created another cache lock")
	}
	if _, err := os.Stat(filepath.Join(cache.Name(), key+".lock")); !os.IsNotExist(err) {
		t.Fatal("old route lock remained")
	}
}

func TestInterruptedLockMigrationDoesNotOverwriteConflicts(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		dir := layoutDir(t)
		for _, name := range []string{"action.lock", "config.lock"} {
			f, err := regular(dir, name, true)
			if err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
		f, err := open(dir, "config.lock", "config.lock", false, sameBoot("boot-a"))
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		original, _ := os.Stat(filepath.Join(dir.Name(), "action.lock"))
		destination := filepath.Join(dir.Name(), "locks", "action.lock")
		if conflict {
			if err := os.WriteFile(destination, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Rename(filepath.Join(dir.Name(), "action.lock"), destination); err != nil {
			t.Fatal(err)
		}
		f, err = open(dir, "config.lock", "config.lock", false, sameBoot("boot-b"))
		if conflict {
			if err == nil {
				f.Close()
				t.Fatal("migration overwrote an existing destination")
			}
			got, _ := os.ReadFile(destination)
			if string(got) != "keep" {
				t.Fatal("destination changed")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			f.Close()
			moved, _ := os.Stat(destination)
			if !os.SameFile(original, moved) {
				t.Fatal("interrupted migration lost the inode")
			}
		}
	}
}
