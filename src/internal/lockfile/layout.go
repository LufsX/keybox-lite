// Package lockfile keeps independent flock files in private lock directories.
package lockfile

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var runtimeLocks = map[string]string{
	"action.lock":         "action.lock",
	"config.lock":         "config.lock",
	"scheduler.lock":      "scheduler.lock",
	"maintenance.lock":    "maintenance.lock",
	"update-control.lock": "update-control.lock",
	"targets.lock":        "targets/lock",
	"files.lock":          "keybox-files/files.lock",
}

func Open(state, name string) (*os.File, error) {
	return OpenContext(context.Background(), state, name)
}

func OpenContext(ctx context.Context, state, name string) (*os.File, error) {
	dir, err := openDir(state)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return OpenAtContext(ctx, dir, name)
}

func OpenAt(state *os.File, name string) (*os.File, error) {
	return OpenAtContext(context.Background(), state, name)
}

func OpenAtContext(ctx context.Context, state *os.File, name string) (*os.File, error) {
	legacy, ok := runtimeLocks[name]
	if !ok {
		return nil, fmt.Errorf("unknown runtime lock")
	}
	return openContext(ctx, state, name, legacy, false, bootID)
}

func OpenCache(dir *os.File, key string) (*os.File, error) {
	return OpenCacheContext(context.Background(), dir, key)
}

func OpenCacheContext(ctx context.Context, dir *os.File, key string) (*os.File, error) {
	if !cacheKey(key) {
		return nil, fmt.Errorf("invalid cache lock key")
	}
	return openContext(ctx, dir, "status.lock", key+".lock", true, bootID)
}

func bootID() (string, error) {
	raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(raw))
	if id == "" || len(id) > 128 || strings.ContainsAny(id, " \t\r\n/") {
		return "", fmt.Errorf("invalid boot identity")
	}
	return id, nil
}

func cacheKey(key string) bool {
	return len(key) == 64 && strings.Trim(key, "0123456789abcdef") == ""
}

func openDir(path string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("absolute state directory required")
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, name := range strings.Split(filepath.Clean(path), "/") {
		if name == "" {
			continue
		}
		next, e := syscall.Openat(fd, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		syscall.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), path), nil
}

func secureDir(dir *os.File) error {
	st, err := dir.Stat()
	if err != nil {
		return err
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.IsDir() || stat.Uid != uint32(os.Geteuid()) || st.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("unsafe lock directory")
	}
	return dir.Chmod(0700)
}

func child(dir *os.File, name string, create bool) (*os.File, error) {
	if create {
		if err := syscall.Mkdirat(int(dir.Fd()), name, 0700); err != nil && !errors.Is(err, syscall.EEXIST) {
			return nil, err
		}
	}
	fd, err := syscall.Openat(int(dir.Fd()), name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	result := os.NewFile(uintptr(fd), filepath.Join(dir.Name(), name))
	if err := secureDir(result); err != nil {
		result.Close()
		return nil, err
	}
	return result, nil
}

func regular(dir *os.File, name string, create bool) (*os.File, error) {
	flags := syscall.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	if create {
		flags |= syscall.O_CREAT
	}
	fd, err := syscall.Openat(int(dir.Fd()), name, flags, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), filepath.Join(dir.Name(), name))
	st, err := f.Stat()
	if err == nil {
		stat, ok := st.Sys().(*syscall.Stat_t)
		if !ok || !st.Mode().IsRegular() || stat.Mode&07777 != 0600 ||
			stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
			err = fmt.Errorf("unsafe lock file")
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func parent(dir *os.File, relative string, create bool) (*os.File, string, error) {
	parts := strings.Split(relative, "/")
	if len(parts) == 1 {
		return nil, parts[0], nil
	}
	if len(parts) != 2 {
		return nil, "", fmt.Errorf("invalid legacy lock path")
	}
	p, err := child(dir, parts[0], create)
	return p, parts[1], err
}

func existing(dir *os.File, relative string) (*os.File, error) {
	p, name, err := parent(dir, relative, false)
	if err != nil {
		return nil, err
	}
	if p != nil {
		defer p.Close()
		dir = p
	}
	return regular(dir, name, false)
}

func legacyPaths(dir *os.File, cache bool) (map[string]string, error) {
	if !cache {
		return runtimeLocks, nil
	}
	// A separate description avoids sharing the caller's directory offset.
	scan, err := child(dir, ".", false)
	if err != nil {
		return nil, err
	}
	defer scan.Close()
	names, err := scan.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	paths := map[string]string{}
	for _, name := range names {
		if strings.HasSuffix(name, ".lock") && cacheKey(strings.TrimSuffix(name, ".lock")) {
			paths[name] = name
		}
	}
	return paths, nil
}

func readLayout(dir *os.File) ([]string, error) {
	f, err := regular(dir, "layout", false)
	if errors.Is(err, syscall.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 513))
	fields := strings.Fields(string(raw))
	if err != nil {
		return nil, err
	}
	if len(raw) > 512 || len(fields) != 3 || fields[0] != "1" || (fields[1] != "ready" && fields[1] != "pending") {
		return nil, fmt.Errorf("invalid lock layout")
	}
	return fields, nil
}

func writeLayout(dir *os.File, phase, boot string) error {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	name := ".layout-" + hex.EncodeToString(nonce[:])
	fd, err := syscall.Openat(int(dir.Fd()), name, syscall.O_CREAT|syscall.O_EXCL|syscall.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	defer syscall.Unlinkat(int(dir.Fd()), name)
	_, err = fmt.Fprintf(f, "1 %s %s\n", phase, boot)
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
	if err = syscall.Renameat(int(dir.Fd()), name, int(dir.Fd()), "layout"); err != nil {
		return err
	}
	return dir.Sync()
}

func openContext(ctx context.Context, dir *os.File, name, legacy string, cache bool, currentBoot func() (string, error)) (*os.File, error) {
	if err := secureDir(dir); err != nil {
		return nil, err
	}
	locks, err := child(dir, "locks", true)
	if err != nil {
		return nil, err
	}
	defer locks.Close()
	// The committed layout is immutable. Ordinary calls need no layout lock.
	layout, err := readLayout(locks)
	if err != nil {
		return nil, err
	}
	if layout != nil && layout[1] == "ready" {
		return regular(locks, name, true)
	}
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		err = syscall.Flock(int(locks.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN && err != syscall.EINTR {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(locks.Fd()), syscall.LOCK_UN)
	layout, err = readLayout(locks)
	if err != nil {
		return nil, err
	}
	if layout == nil || layout[1] == "pending" {
		boot, err := currentBoot()
		if err != nil {
			return nil, err
		}
		paths, err := legacyPaths(dir, cache)
		if err != nil {
			return nil, err
		}
		found := false
		for _, relative := range paths {
			f, e := existing(dir, relative)
			if errors.Is(e, syscall.ENOENT) {
				continue
			}
			if e != nil {
				return nil, e
			}
			f.Close()
			found = true
		}
		if layout == nil {
			phase := "ready"
			if found {
				phase = "pending"
			}
			if err = writeLayout(locks, phase, boot); err != nil {
				return nil, err
			}
			layout = []string{"1", phase, boot}
		}
		if layout[1] == "pending" && layout[2] != boot {
			// Previous-boot holders and waiters no longer exist. Preserve fixed
			// lock inodes and retire route-specific cache locks at this boundary.
			for canonical, relative := range paths {
				src, base, e := parent(dir, relative, false)
				if errors.Is(e, syscall.ENOENT) {
					continue
				}
				if e != nil {
					return nil, e
				}
				source := dir
				if src != nil {
					source = src
				}
				f, e := regular(source, base, false)
				if errors.Is(e, syscall.ENOENT) {
					if src != nil {
						src.Close()
					}
					continue
				}
				if e != nil {
					if src != nil {
						src.Close()
					}
					return nil, e
				}
				f.Close()
				if cache {
					e = syscall.Unlinkat(int(source.Fd()), base)
				} else {
					dest, destErr := regular(locks, canonical, false)
					if destErr == nil {
						dest.Close()
						e = fmt.Errorf("lock migration destination already exists")
					} else if !errors.Is(destErr, syscall.ENOENT) {
						e = destErr
					} else {
						e = syscall.Renameat(int(source.Fd()), base, int(locks.Fd()), canonical)
					}
				}
				if e == nil {
					e = source.Sync()
				}
				if src != nil {
					src.Close()
				}
				if e != nil {
					return nil, e
				}
			}
			if err = locks.Sync(); err != nil {
				return nil, err
			}
			if err = writeLayout(locks, "ready", boot); err != nil {
				return nil, err
			}
			layout[1] = "ready"
		}
	}
	if layout[1] == "pending" {
		p, base, err := parent(dir, legacy, true)
		if err != nil {
			return nil, err
		}
		if p != nil {
			defer p.Close()
			dir = p
		}
		return regular(dir, base, true)
	}
	return regular(locks, name, true)
}
