package verify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestCacheRejectsUnsafeDirectParent(t *testing.T) {
	for _, mode := range []os.FileMode{0771, 0770, 0702, 0777 | os.ModeSticky} {
		t.Run(mode.String(), func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "state")
			if err := os.Mkdir(state, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(state, mode); err != nil {
				t.Fatal(err)
			}
			for _, exists := range []bool{false, true} {
				path := filepath.Join(state, "cache")
				if exists {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				}
				dir, err := openCacheDir(path)
				if dir != nil {
					dir.Close()
				}
				if !errors.Is(err, errCacheParent) {
					t.Fatalf("existing=%v: unsafe parent accepted: %v", exists, err)
				}
				if !exists {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatalf("created cache under unsafe parent: %v", err)
					}
				}
			}
		})
	}
}

func TestCacheRejectsUntrustedParentOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("owner change requires root")
	}
	state := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(state, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	dir, err := openCacheDir(filepath.Join(state, "cache"))
	if dir != nil {
		dir.Close()
	}
	if !errors.Is(err, errCacheParent) {
		t.Fatalf("untrusted owner accepted: %v", err)
	}
}

func TestCacheRejectsMissingBoundaryUnderWritableAncestor(t *testing.T) {
	ancestor := filepath.Join(t.TempDir(), "android-data")
	if err := os.Mkdir(ancestor, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ancestor, 0771); err != nil {
		t.Fatal(err)
	}
	dir, err := openCacheDir(filepath.Join(ancestor, "state", "cache"))
	if dir != nil {
		dir.Close()
	}
	if !errors.Is(err, errCacheParent) {
		t.Fatalf("created trust boundary in writable ancestor: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(ancestor, "state")); !os.IsNotExist(err) {
		t.Fatalf("untrusted boundary created: %v", err)
	}
}

func TestCacheDescriptorSurvivesAncestorReplacement(t *testing.T) {
	base := t.TempDir()
	ancestor := filepath.Join(base, "ancestor")
	path := filepath.Join(ancestor, "state", "cache")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := openCacheDir(path)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	moved := filepath.Join(base, "moved")
	if err := os.Rename(ancestor, moved); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, ancestor); err != nil {
		t.Fatal(err)
	}
	result := statusDownload{raw: []byte(`{"entries":{}}`), url: live}
	if err := writeStatusCache(dir, "record", result); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := readStatusCache(dir, "record", " ", time.Now()); err != nil || !ok {
		t.Fatalf("descriptor lost original cache: %v", err)
	}
	if _, err := os.Stat(filepath.Join(moved, "state", "cache", "record")); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
		t.Fatalf("replacement target modified: %v, %v", entries, err)
	}
	reopened, err := openCacheDir(path)
	if reopened != nil {
		reopened.Close()
		t.Fatal("ancestor symlink accepted")
	}
	if err == nil {
		t.Fatal("missing symlink error")
	}
}

func TestCacheErrorReasonRedactsDetails(t *testing.T) {
	err := &os.PathError{Op: "open", Path: "/private/secret", Err: syscall.EACCES}
	if got := cacheErrorReason(err); got != syscall.EACCES.Error() {
		t.Fatalf("unexpected errno reason: %q", got)
	}
	if got := cacheErrorReason(errors.New("sensitive downloaded content")); got != "cache operation failed" {
		t.Fatalf("unexpected generic reason: %q", got)
	}
}

func TestCachePrivatePermissions(t *testing.T) {
	dir, file := cacheFixture(t)
	if _, ok := fetchStatus(context.Background(), dir, " "); !ok {
		t.Fatal("fetch failed")
	}
	lock := cacheLockPath(t)
	for path, want := range map[string]os.FileMode{
		cachePath(): 0700, file: 0600, filepath.Dir(lock): 0700, lock: 0600,
	} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s: permissions %v, %v", path, info, err)
		}
	}
}

func TestCacheExpiredFailureRemovesOldRecord(t *testing.T) {
	dir, file := cacheFixture(t)
	if _, ok := fetchStatus(context.Background(), dir, " "); !ok {
		t.Fatal("initial fetch failed")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var record cachedStatus
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	record.Fetched = time.Now().Add(-2 * statusCacheTTL).UnixNano()
	raw, _ = json.Marshal(record)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VERIFY_CACHE_MODE", "failed")
	if _, ok := fetchStatus(context.Background(), dir, " "); ok {
		t.Fatal("expired clean cache masked live failure")
	}
	if _, err := os.Lstat(file); !os.IsNotExist(err) {
		t.Fatalf("expired record survived failed refresh: %v", err)
	}
}

func TestCacheAtomicWriteFailureCleanup(t *testing.T) {
	cacheFixture(t)
	dir, err := openCacheDir(cachePath())
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	// Renaming a regular file onto this directory must fail after writing the temp.
	if err := syscall.Mkdirat(int(dir.Fd()), "destination", 0700); err != nil {
		t.Fatal(err)
	}
	result := statusDownload{raw: []byte(`{"entries":{}}`), url: live}
	if err := writeStatusCache(dir, "destination", result); err == nil {
		t.Fatal("rename unexpectedly succeeded")
	}
	entries, err := os.ReadDir(cachePath())
	if err != nil || len(entries) != 1 || entries[0].Name() != "destination" {
		t.Fatalf("temporary cache file leaked: %v, %v", entries, err)
	}
	// Refuse invalid and mirror records even when called independently.
	for _, invalid := range []statusDownload{
		{raw: []byte(`{"entries":null}`), url: live},
		{raw: []byte(`{"entries":{}}`), url: mirror, stale: true},
	} {
		if err := writeStatusCache(dir, "bad.json", invalid); err == nil {
			t.Fatal("invalid result written")
		}
		if _, err := os.Lstat(filepath.Join(cachePath(), "bad.json")); !os.IsNotExist(err) {
			t.Fatalf("invalid record created: %v", err)
		}
	}
}
