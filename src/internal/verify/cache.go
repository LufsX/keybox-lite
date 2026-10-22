package verify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"keyboxlite/internal/status"
	"keyboxlite/internal/termfmt"
)

const (
	defaultStatusCache = "/data/adb/keybox-lite/status-cache"
	statusCacheTTL     = 60 * time.Second
	cacheLockTimeout   = 35 * time.Second
	maxCacheBytes      = maxStatusBytes + 4096
)

var (
	errCachePath   = errors.New("cache path must be absolute and non-root")
	errCacheParent = errors.New("unsafe cache parent owner or permissions")
	errCacheDir    = errors.New("cache directory requires current owner and mode 0700")
	errCacheFile   = errors.New("cache file requires regular type, current owner, mode 0600 and one link")
)

type cachedStatus struct {
	Version int             `json:"version"`
	Fetched int64           `json:"fetched"`
	URL     string          `json:"url"`
	Raw     json.RawMessage `json:"raw"`
}

func cacheName(accel string) string {
	// Cache the list, never a keybox or the result of checking its serials.
	// Different effective proxy configurations have independent trust routes.
	sum := sha256.Sum256([]byte("v1\n" + strings.Join(statusURLs(accel, live), "\n")))
	return hex.EncodeToString(sum[:])
}

func cachePath() string {
	if dir := os.Getenv("KB_STATUS_CACHE"); dir != "" {
		return dir
	}
	return defaultStatusCache
}

// Walk every component with NOFOLLOW and retain directory descriptors. The direct
// parent is the trust boundary: root/current owner, with no group/world writes.
// Higher ancestors may have Android system ownership and writable groups.
func openCacheDir(path string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, errCachePath
	}
	path = filepath.Clean(path)
	if path == "/" {
		return nil, errCachePath
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	current := os.NewFile(uintptr(fd), "/")
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		if i == len(parts)-1 {
			if err := checkCacheParent(current); err != nil {
				current.Close()
				return nil, err
			}
		}
		next, err := syscall.Openat(int(current.Fd()), part,
			syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if errors.Is(err, syscall.ENOENT) {
			// Never create a trust boundary inside an untrusted writable directory.
			if err := checkCacheParent(current); err != nil {
				current.Close()
				return nil, err
			}
			if err = syscall.Mkdirat(int(current.Fd()), part, 0700); err == nil || errors.Is(err, syscall.EEXIST) {
				next, err = syscall.Openat(int(current.Fd()), part,
					syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
			}
		}
		current.Close()
		if err != nil {
			return nil, err
		}
		current = os.NewFile(uintptr(next), part)
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(int(current.Fd()), &st); err != nil {
		current.Close()
		return nil, err
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0700 {
		current.Close()
		return nil, errCacheDir
	}
	return current, nil
}

func checkCacheParent(dir *os.File) error {
	var st syscall.Stat_t
	if err := syscall.Fstat(int(dir.Fd()), &st); err != nil {
		return err
	}
	if (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) || st.Mode&0022 != 0 {
		return errCacheParent
	}
	return nil
}

// cacheErrorReason never includes paths, downloaded content or configuration.
func cacheErrorReason(err error) string {
	for _, reason := range []error{errCachePath, errCacheParent, errCacheDir, errCacheFile} {
		if errors.Is(err, reason) {
			return reason.Error()
		}
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno.Error()
	}
	return "cache operation failed"
}

// O_NONBLOCK prevents a substituted FIFO from blocking before the type check.
func openCacheFile(dir *os.File, name string, flags int) (*os.File, error) {
	fd, err := syscall.Openat(int(dir.Fd()), name,
		flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	var st syscall.Stat_t
	if err = syscall.Fstat(fd, &st); err == nil {
		if st.Mode&syscall.S_IFMT != syscall.S_IFREG || st.Mode&07777 != 0600 ||
			st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
			err = errCacheFile
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func waitCacheLock(ctx context.Context, f *os.File) error {
	ctx, cancel := context.WithTimeout(ctx, cacheLockTimeout)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func readStatusCache(dir *os.File, name, accel string, now time.Time) (statusDownload, bool, error) {
	f, err := openCacheFile(dir, name, syscall.O_RDONLY)
	if errors.Is(err, syscall.ENOENT) {
		return statusDownload{}, false, nil
	}
	if err != nil {
		return statusDownload{}, false, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxCacheBytes+1))
	if err != nil {
		return statusDownload{}, false, err
	}
	var record cachedStatus
	if len(raw) > maxCacheBytes || json.Unmarshal(raw, &record) != nil ||
		record.Version != 1 || record.Fetched <= 0 || len(record.Raw) > maxStatusBytes {
		return statusDownload{}, false, nil
	}
	age := now.Sub(time.Unix(0, record.Fetched))
	if age < 0 || age >= statusCacheTTL {
		return statusDownload{}, false, nil
	}
	validURL := false
	for _, url := range statusURLs(accel, live) {
		if record.URL == url {
			validURL = true
			break
		}
	}
	if !validURL {
		return statusDownload{}, false, nil
	}
	if _, err := status.Check(record.Raw, nil); err != nil {
		return statusDownload{}, false, nil
	}
	return statusDownload{raw: record.Raw, url: record.URL, cached: true}, true, nil
}

func writeStatusCache(dir *os.File, name string, result statusDownload) error {
	if result.stale {
		return errors.New("cannot cache stale mirror")
	}
	if _, err := status.Check(result.raw, nil); err != nil {
		return err
	}
	raw, err := json.Marshal(cachedStatus{Version: 1, Fetched: time.Now().UnixNano(), URL: result.url, Raw: result.raw})
	if err != nil {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	tmp := ".status-" + hex.EncodeToString(nonce[:])
	f, err := openCacheFile(dir, tmp, syscall.O_CREAT|syscall.O_EXCL|syscall.O_WRONLY)
	if err != nil {
		return err
	}
	defer syscall.Unlinkat(int(dir.Fd()), tmp)
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return syscall.Renameat(int(dir.Fd()), tmp, int(dir.Fd()), name)
}

func fetchStatus(ctx context.Context, workdir, accel string) (statusDownload, bool) {
	withoutCache := func(stage string, err error) (statusDownload, bool) {
		if ctx.Err() != nil {
			return statusDownload{}, false
		}
		termfmt.Printf("[CACHE] unavailable (%s: %s); fetching live\n", stage, cacheErrorReason(err))
		return fetchStatusUncached(ctx, workdir, accel)
	}
	dir, err := openCacheDir(cachePath())
	if err != nil {
		return withoutCache("directory", err)
	}
	defer dir.Close()
	key := cacheName(accel)
	lock, err := openCacheFile(dir, key+".lock", syscall.O_CREAT|syscall.O_RDWR)
	if err != nil {
		return withoutCache("lock file", err)
	}
	defer lock.Close()
	if err := waitCacheLock(ctx, lock); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			termfmt.Printf("[FAIL] status cache lock: %v\n", err)
			return statusDownload{}, false
		}
		return withoutCache("lock", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	name := key + ".json"
	result, ok, err := readStatusCache(dir, name, accel, time.Now())
	if err != nil {
		return withoutCache("read", err)
	}
	if ok {
		return result, true
	}
	// Remove expired/malformed records before downloading, including on failure.
	if err := syscall.Unlinkat(int(dir.Fd()), name); err != nil && !errors.Is(err, syscall.ENOENT) {
		return withoutCache("remove old record", err)
	}
	result, ok = fetchStatusUncached(ctx, workdir, accel)
	if ok && !result.stale {
		if err := writeStatusCache(dir, name, result); err != nil {
			termfmt.Printf("[CACHE] unavailable (write: %s); live result not cached\n", cacheErrorReason(err))
		}
	}
	return result, ok
}
