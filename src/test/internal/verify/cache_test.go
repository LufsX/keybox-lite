package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Both helpers run only in subprocesses and never access the network.
func TestCacheNetworkProcess(t *testing.T) {
	if os.Getenv("VERIFY_CACHE_NETWORK") != "1" {
		return
	}
	log, err := os.OpenFile(os.Getenv("VERIFY_CACHE_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(2)
	}
	fmt.Fprintln(log, os.Getpid())
	log.Close()
	time.Sleep(100 * time.Millisecond)
	mode := os.Getenv("VERIFY_CACHE_MODE")
	switch mode {
	case "invalid":
		fmt.Print(`{"entries":null}`)
	case "failed":
		fmt.Print(`{"entries":{}}`)
		os.Exit(1)
	case "blocked":
		time.Sleep(10 * time.Second)
	default:
		fmt.Print(`{"entries":{"2a":{"status":"REVOKED"}}}`)
	}
	os.Exit(0)
}

func TestCacheVerifyProcess(t *testing.T) {
	if os.Getenv("VERIFY_CACHE_CHILD") == "1" {
		os.Exit(Run([]string{os.Getenv("VERIFY_CACHE_KEYBOX")}))
	}
}

func cacheFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\nexec \"$VERIFY_CACHE_EXE\" -test.run=^TestCacheNetworkProcess$ -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("TMPDIR", dir)
	t.Setenv("KB_STATUS_CACHE", filepath.Join(dir, "cache"))
	t.Setenv("KB_ACCEL", " ")
	t.Setenv("VERIFY_CACHE_EXE", exe)
	t.Setenv("VERIFY_CACHE_LOG", filepath.Join(dir, "requests"))
	t.Setenv("VERIFY_CACHE_NETWORK", "1")
	t.Setenv("VERIFY_CACHE_MODE", "live")
	return dir, filepath.Join(dir, "cache", cacheName(" ")+".json")
}

func requestCount(t *testing.T) int {
	t.Helper()
	raw, err := os.ReadFile(os.Getenv("VERIFY_CACHE_LOG"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(raw)))
}

func TestCacheAndroidAncestorsHit(t *testing.T) {
	dir, _ := cacheFixture(t)
	// Simulate /data/adb/state entirely under the test's temporary directory.
	ancestor := filepath.Join(dir, "android-data")
	state := filepath.Join(ancestor, "adb", "state")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(ancestor, 1000, 1000); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(ancestor, 0771); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KB_STATUS_CACHE", filepath.Join(state, "cache"))
	first, ok := fetchStatus(context.Background(), dir, " ")
	if !ok || first.cached {
		t.Fatal("initial live fetch failed")
	}
	second, ok := fetchStatus(context.Background(), dir, " ")
	if !ok || !second.cached || !bytes.Equal(first.raw, second.raw) || requestCount(t) != 1 {
		t.Fatal("Android ancestor prevented cache hit")
	}
	info, err := os.Stat(cachePath())
	if err != nil || info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		t.Fatalf("cache is not private: %v, %v", info, err)
	}
}

func TestCacheUnavailableDiagnostic(t *testing.T) {
	dir, _ := cacheFixture(t)
	state := filepath.Join(dir, "private-path-must-not-appear")
	if err := os.Mkdir(state, 0770); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0770); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KB_STATUS_CACHE", filepath.Join(state, "cache"))
	t.Setenv("VERIFY_CACHE_CHILD", "1")
	t.Setenv("VERIFY_CACHE_KEYBOX", testKeyboxSerial(t, dir, 41))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestCacheVerifyProcess$")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("live fallback failed: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("[CACHE] unavailable (directory: unsafe cache parent owner or permissions); fetching live")) {
		t.Fatalf("missing cache failure reason: %s", out)
	}
	if bytes.Contains(out, []byte(state)) || requestCount(t) != 1 {
		t.Fatalf("unexpected fallback output or request count: %s", out)
	}
}

func TestCacheRechecksCurrentKeybox(t *testing.T) {
	dir, file := cacheFixture(t)
	path := testKeyboxSerial(t, dir, 41)
	if got := Run([]string{path}); got != 0 {
		t.Fatalf("first serial: %d", got)
	}
	path = testKeyboxSerial(t, dir, 42)
	if got := Run([]string{path}); got != 1 {
		t.Fatalf("cached list failed to revoke new serial: %d", got)
	}
	if got := requestCount(t); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
	raw, err := os.ReadFile(file)
	if err != nil || bytes.Contains(raw, []byte("PRIVATE")) || bytes.Contains(raw, []byte("AndroidAttestation")) {
		t.Fatalf("unexpected cache content: %v", err)
	}
}

func TestCacheSingleFlightAcrossProcesses(t *testing.T) {
	dir, _ := cacheFixture(t)
	path := testKeyboxSerial(t, dir, 41)
	t.Setenv("VERIFY_CACHE_CHILD", "1")
	t.Setenv("VERIFY_CACHE_KEYBOX", path)
	exe, _ := os.Executable()
	const count = 8
	cmds := make([]*exec.Cmd, count)
	outputs := make([]bytes.Buffer, count)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := range cmds {
		cmds[i] = exec.CommandContext(ctx, exe, "-test.run=^TestCacheVerifyProcess$")
		cmds[i].Stdout = &outputs[i]
		cmds[i].Stderr = &outputs[i]
		if err := cmds[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	hits := 0
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Errorf("child %d: %v\n%s", i, err, &outputs[i])
		}
		out := outputs[i].String()
		if strings.Contains(out, "status: cache hit") {
			hits++
		}
		for _, expected := range []string{"[CHECK] keybox: keybox.xml", "[OK] validity: valid (1 certificates)", "[OK] revocation: 1 certs checked, none revoked", "RESULT: not revoked"} {
			if !strings.Contains(out, expected) {
				t.Errorf("child log missing %q: %s", expected, out)
			}
		}
		if strings.Contains(out, dir) || strings.Contains(out, "validity: serial") {
			t.Errorf("verbose path/serial in successful log: %s", out)
		}
	}
	if got := requestCount(t); got != 1 || hits != count-1 {
		t.Fatalf("requests = %d, cache hits = %d", got, hits)
	}
}

func TestCacheInvalidRecordsRefresh(t *testing.T) {
	for _, name := range []string{"expired", "boundary", "future", "malformed", "invalid status", "mirror", "wrong version", "oversize"} {
		t.Run(name, func(t *testing.T) {
			dir, file := cacheFixture(t)
			first, ok := fetchStatus(context.Background(), dir, " ")
			if !ok || first.cached {
				t.Fatal("initial download failed")
			}
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var record cachedStatus
			if err := json.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "expired":
				record.Fetched = time.Now().Add(-statusCacheTTL - time.Second).UnixNano()
			case "boundary":
				record.Fetched = time.Now().Add(-statusCacheTTL).UnixNano()
			case "future":
				record.Fetched = time.Now().Add(time.Hour).UnixNano()
			case "invalid status":
				record.Raw = json.RawMessage(`{"entries":{"2a":{}}}`)
			case "mirror":
				record.URL = mirror
			case "wrong version":
				record.Version = 2
			}
			raw, _ = json.Marshal(record)
			if name == "malformed" {
				raw = []byte("{")
			}
			if name == "oversize" {
				raw = bytes.Repeat([]byte(" "), maxCacheBytes+1)
			}
			if err := os.WriteFile(file, raw, 0600); err != nil {
				t.Fatal(err)
			}
			got, ok := fetchStatus(context.Background(), dir, " ")
			if !ok || got.cached || requestCount(t) != 2 {
				t.Fatalf("invalid record reused: %+v, %v, requests %d", got, ok, requestCount(t))
			}
			got, ok = fetchStatus(context.Background(), dir, " ")
			if !ok || !got.cached || requestCount(t) != 2 {
				t.Fatal("refreshed list not cached")
			}
		})
	}
}

func TestCacheDoesNotSaveInvalidOrFailedOfficialResponses(t *testing.T) {
	for _, mode := range []string{"invalid", "failed"} {
		t.Run(mode, func(t *testing.T) {
			dir, file := cacheFixture(t)
			t.Setenv("VERIFY_CACHE_MODE", mode)
			path := testKeyboxSerial(t, dir, 41)
			// cacheFixture configures no proxy (" ") and only a curl stub, so an
			// unusable response must not be cached and must not suppress the next
			// official attempt: each Run issues exactly len(downloaders()) *
			// len(statusURLs(" ", live)) requests.
			perRun := len(downloaders()) * len(statusURLs(" ", live))
			for i := 1; i <= 2; i++ {
				if got := Run([]string{path}); got != 2 {
					t.Fatalf("Run = %d, want 2", got)
				}
				if _, err := os.Lstat(file); !os.IsNotExist(err) {
					t.Fatalf("unusable official response cached: %v", err)
				}
				if got := requestCount(t); got != perRun*i {
					t.Fatalf("requests after run %d = %d, want %d", i, got, perRun*i)
				}
			}
		})
	}
}

func TestCacheConfigurationKey(t *testing.T) {
	dir, _ := cacheFixture(t)
	for _, accel := range []string{" ", "https://proxy.invalid/"} {
		result, ok := fetchStatus(context.Background(), dir, accel)
		if !ok || result.cached {
			t.Fatalf("configuration reused another cache: %v, %v", result, ok)
		}
	}
	if cacheName("https://proxy/ https://proxy/") != cacheName("https://proxy/") {
		t.Fatal("equivalent URL configurations use different cache keys")
	}
}

func TestCacheRejectsUnsafePaths(t *testing.T) {
	for _, kind := range []string{"directory symlink", "ancestor symlink", "directory mode", "file mode", "file symlink", "lock symlink", "hardlink", "fifo", "lock mode", "directory file", "owner"} {
		t.Run(kind, func(t *testing.T) {
			dir, file := cacheFixture(t)
			if _, ok := fetchStatus(context.Background(), dir, " "); !ok {
				t.Fatal("initial fetch failed")
			}
			cache := os.Getenv("KB_STATUS_CACHE")
			lock := strings.TrimSuffix(file, ".json") + ".lock"
			target := filepath.Join(dir, "protected")
			if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "directory file":
				t.Setenv("KB_STATUS_CACHE", target)
			case "directory symlink", "ancestor symlink":
				link := filepath.Join(dir, "linked")
				if err := os.Symlink(cache, link); err != nil {
					t.Fatal(err)
				}
				if kind == "ancestor symlink" {
					link = filepath.Join(link, "child")
				}
				t.Setenv("KB_STATUS_CACHE", link)
			case "directory mode":
				if err := os.Chmod(cache, 0755); err != nil {
					t.Fatal(err)
				}
			case "file mode":
				if err := os.Chmod(file, 0644); err != nil {
					t.Fatal(err)
				}
			case "lock mode":
				if err := os.Chmod(lock, 0666); err != nil {
					t.Fatal(err)
				}
			case "file symlink", "lock symlink":
				name := file
				if kind == "lock symlink" {
					name = lock
				}
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, name); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(target, file); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Mkfifo(file, 0600); err != nil {
					t.Fatal(err)
				}
			case "owner":
				if os.Geteuid() != 0 {
					t.Skip("owner change requires root")
				}
				if err := os.Chown(file, 12345, -1); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result, ok := fetchStatus(ctx, dir, " ")
			if !ok || result.cached {
				t.Fatal("unsafe cache prevented independent live check")
			}
			if requestCount(t) != 2 {
				t.Fatal("unsafe cache reused instead of fetching live")
			}
			raw, err := os.ReadFile(target)
			if err != nil || string(raw) != "untouched" {
				t.Fatal("symlink/hardlink target modified")
			}
		})
	}
}

func TestCacheLockCancellationAndRelease(t *testing.T) {
	dir, _ := cacheFixture(t)
	cache, err := openCacheDir(cachePath())
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	lock, err := openCacheFile(cache, cacheName(" ")+".lock", syscall.O_CREAT|syscall.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, ok := fetchStatus(ctx, dir, " "); ok {
		t.Fatal("ignored locked cache")
	}
	if time.Since(start) > time.Second || requestCount(t) != 0 {
		t.Fatal("lock cancellation not bounded")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if _, ok := fetchStatus(context.Background(), dir, " "); !ok {
		t.Fatal("lock not released")
	}
}

func TestCacheCanceledDownloadCleansUp(t *testing.T) {
	dir, file := cacheFixture(t)
	t.Setenv("VERIFY_CACHE_MODE", "blocked")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, ok := fetchStatus(ctx, dir, " "); ok {
		t.Fatal("canceled response accepted")
	}
	log, err := os.ReadFile(os.Getenv("VERIFY_CACHE_LOG"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range strings.Fields(string(log)) {
		pid, _ := strconv.Atoi(value)
		if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
			t.Fatalf("child %d still exists: %v", pid, err)
		}
	}
	if _, err := os.Lstat(file); !os.IsNotExist(err) {
		t.Fatal("canceled result cached")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range files {
		if strings.HasPrefix(entry.Name(), "status-") {
			t.Fatalf("download file leaked: %s", entry.Name())
		}
	}
	entries, err := os.ReadDir(cachePath())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".lock") {
			t.Fatalf("cache temp leaked: %s", entry.Name())
		}
	}
	t.Setenv("VERIFY_CACHE_MODE", "live")
	if _, ok := fetchStatus(context.Background(), dir, " "); !ok {
		t.Fatal("canceled download retained lock")
	}
}
