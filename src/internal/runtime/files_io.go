package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"keyboxlite/internal/lockfile"
)

const (
	filesMaxBytes     = 1 << 20
	filesChunkBytes   = 24 << 10
	filesSessionLimit = 8
	filesTTL          = time.Hour
	filesBudget       = 20 * time.Minute
)

// Walk every directory through descriptors. O_NOFOLLOW on only the final
// component would still allow an ancestor to be exchanged for a symlink.
func filesOpenDir(path string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("unsafe directory")
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot open directory")
	}
	for _, part := range strings.Split(filepath.Clean(path), "/") {
		if part == "" {
			continue
		}
		next, openErr := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		syscall.Close(fd)
		if openErr != nil {
			return nil, fmt.Errorf("unsafe or unavailable directory")
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), path), nil
}

func filesOpen(dir *os.File, name string, flags int, private bool) (*os.File, error) {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return nil, fmt.Errorf("invalid file identifier")
	}
	openFlags := flags | syscall.O_CLOEXEC | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	created := false
	var fd int
	var err error
	if flags&syscall.O_CREAT != 0 {
		fd, err = syscall.Openat(int(dir.Fd()), name, openFlags|syscall.O_EXCL, 0600)
		created = err == nil
		if err == syscall.EEXIST && flags&syscall.O_EXCL == 0 {
			fd, err = syscall.Openat(int(dir.Fd()), name, openFlags & ^syscall.O_CREAT, 0)
		}
	} else {
		fd, err = syscall.Openat(int(dir.Fd()), name, openFlags, 0)
	}
	if err != nil {
		if err == syscall.ENOENT {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("unsafe or unavailable file")
	}
	f := os.NewFile(uintptr(fd), name)
	if created {
		if err = f.Chmod(0600); err != nil {
			f.Close()
			filesUnlink(dir, name)
			return nil, fmt.Errorf("cannot secure private file")
		}
	}
	st, err := f.Stat()
	if err != nil || !filesRegular(st) {
		f.Close()
		return nil, fmt.Errorf("file must be regular with one link")
	}
	if private && st.Mode().Perm() != 0600 {
		f.Close()
		return nil, fmt.Errorf("unsafe private file permissions")
	}
	return f, nil
}

func filesRegular(st os.FileInfo) bool {
	if st == nil || !st.Mode().IsRegular() {
		return false
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}

func filesRead(dir *os.File, name string, limit int, private bool) ([]byte, os.FileInfo, error) {
	f, err := filesOpen(dir, name, syscall.O_RDONLY, private)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return nil, nil, fmt.Errorf("cannot inspect file")
	}
	if before.Size() > int64(limit) {
		return nil, before, fmt.Errorf("file exceeds size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	after, statErr := f.Stat()
	if err != nil || statErr != nil || len(raw) > limit || !filesSame(before, after) {
		return nil, before, fmt.Errorf("file changed or cannot be read within size limit")
	}
	return raw, before, nil
}

func filesSame(a, b os.FileInfo) bool {
	if !filesRegular(a) || !filesRegular(b) || !os.SameFile(a, b) ||
		a.Size() != b.Size() || a.Mode() != b.Mode() || !a.ModTime().Equal(b.ModTime()) {
		return false
	}
	x, xok := a.Sys().(*syscall.Stat_t)
	y, yok := b.Sys().(*syscall.Stat_t)
	return xok && yok && x.Ctim == y.Ctim
}

func filesID(id string) bool {
	return len(id) == 64 && strings.Trim(id, "0123456789abcdef") == ""
}

func filesBackupName(id string) bool {
	separator := strings.LastIndex(id, ".bak.")
	if separator <= 0 || filepath.Base(id) != id {
		return false
	}
	suffix := id[separator+len(".bak."):]
	return suffix != "" && strings.Trim(suffix, "0123456789") == ""
}

func filesBackupID(id, target string) bool {
	suffix, ok := strings.CutPrefix(id, filepath.Base(target)+".bak.")
	return ok && suffix != "" && strings.Trim(suffix, "0123456789") == "" && filepath.Base(id) == id
}

func filesUnlink(dir *os.File, name string) error {
	err := syscall.Unlinkat(int(dir.Fd()), name)
	if err == syscall.ENOENT {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot remove private file")
	}
	return nil
}

type filesStore struct {
	dir  *os.File
	lock *os.File
}

func (s *filesStore) close() {
	s.lock.Close()
	s.dir.Close()
}

// Lock order: action.lock, then files.lock. Session-only commands never take
// action.lock. Verification holds action.lock but releases files.lock.
func (e *engine) filesStore(ctx context.Context) (*filesStore, error) {
	if err := e.init(); err != nil {
		return nil, fmt.Errorf("cannot initialize private storage")
	}
	if !e.enabled() {
		return nil, fmt.Errorf("module is disabled or being removed")
	}
	parent, err := filesOpenDir(e.p.state)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	if err = syscall.Mkdirat(int(parent.Fd()), "keybox-files", 0700); err != nil && err != syscall.EEXIST {
		return nil, fmt.Errorf("cannot create private storage")
	}
	fd, err := syscall.Openat(int(parent.Fd()), "keybox-files", syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, fmt.Errorf("unsafe private storage")
	}
	dir := os.NewFile(uintptr(fd), filepath.Join(e.p.state, "keybox-files"))
	if err = dir.Chmod(0700); err != nil {
		dir.Close()
		return nil, fmt.Errorf("cannot secure private storage")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	f, err := lockfile.OpenAtContext(ctx, parent, "files.lock")
	if err != nil {
		dir.Close()
		return nil, err
	}
	until := time.Now().Add(10 * time.Second)
	for {
		if err = ctx.Err(); err != nil {
			break
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			if !e.enabled() {
				err = fmt.Errorf("module is disabled or being removed")
				break
			}
			s := &filesStore{dir: dir, lock: f}
			if err = e.filesExpire(s); err == nil {
				return s, nil
			}
			break
		}
		if (err != syscall.EAGAIN && err != syscall.EWOULDBLOCK) || time.Now().After(until) {
			err = fmt.Errorf("private storage busy")
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(25 * time.Millisecond):
		}
	}
	f.Close()
	dir.Close()
	return nil, err
}

func filesNames(dir *os.File) ([]string, error) {
	// Open "." to get an independent directory offset for each enumeration.
	fd, err := syscall.Openat(int(dir.Fd()), ".", syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot enumerate files")
	}
	f := os.NewFile(uintptr(fd), dir.Name())
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, fmt.Errorf("cannot enumerate files")
	}
	return names, nil
}

func (e *engine) filesExpire(s *filesStore) error {
	names, err := filesNames(s.dir)
	if err != nil {
		return err
	}
	for _, name := range names {
		if suffix, ok := strings.CutPrefix(name, "work."); ok && filesID(suffix) {
			if err := e.filesExpireWork(s, name); err != nil {
				return err
			}
			continue
		}
		id := strings.TrimSuffix(name, ".sealed")
		if !filesID(id) {
			continue
		}
		f, err := filesOpen(s.dir, id, syscall.O_RDONLY, false)
		if os.IsNotExist(err) {
			if err = filesUnlink(s.dir, id+".sealed"); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			continue // Unsafe entries are never followed or treated as sessions.
		}
		st, err := f.Stat()
		f.Close()
		if err != nil {
			return fmt.Errorf("cannot inspect session age")
		}
		if e.d.now().Sub(st.ModTime()) >= filesTTL {
			if err = filesUnlink(s.dir, id+".sealed"); err == nil {
				err = filesUnlink(s.dir, id)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}
