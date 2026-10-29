package runtime

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Keep both directories open: prepare/verify accept paths, but staging and
// identity checks must stay anchored to the private inodes created here.
type filesWork struct {
	parent, dir *os.File
	path        string
	info        os.FileInfo
	candidate   *os.File
	staged      os.FileInfo
}

func (e *engine) filesNewWork(s *filesStore) (*filesWork, error) {
	parent, err := filesOpenDir(s.dir.Name())
	if err != nil {
		return nil, err
	}
	original, err := s.dir.Stat()
	current, statErr := parent.Stat()
	if err != nil || statErr != nil || !os.SameFile(original, current) {
		parent.Close()
		return nil, fmt.Errorf("private storage changed")
	}
	var token [32]byte
	if _, err = rand.Read(token[:]); err != nil {
		parent.Close()
		return nil, fmt.Errorf("cannot create verification identifier")
	}
	name := "work." + hex.EncodeToString(token[:])
	if err = syscall.Mkdirat(int(parent.Fd()), name, 0700); err != nil {
		parent.Close()
		return nil, fmt.Errorf("cannot create private verification directory")
	}
	fd, err := syscall.Openat(int(parent.Fd()), name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		parent.Close()
		return nil, fmt.Errorf("cannot open private verification directory")
	}
	w := &filesWork{parent: parent, dir: os.NewFile(uintptr(fd), name), path: filepath.Join(parent.Name(), name)}
	if err = w.dir.Chmod(0700); err == nil {
		w.info, err = w.dir.Stat()
	}
	if err == nil {
		err = w.checkDir()
	}
	if err != nil {
		w.close()
		return nil, fmt.Errorf("cannot secure private verification directory")
	}
	return w, nil
}

func (w *filesWork) checkDir() error {
	current, err := filesOpenDir(w.path)
	if err != nil {
		return fmt.Errorf("verification directory changed")
	}
	defer current.Close()
	st, err := current.Stat()
	if err != nil || w.info == nil || !os.SameFile(w.info, st) || !st.IsDir() || st.Mode().Perm() != 0700 {
		return fmt.Errorf("verification directory changed")
	}
	return nil
}

func (w *filesWork) stage(data []byte) error {
	if err := w.checkDir(); err != nil {
		return err
	}
	f, err := filesOpen(w.dir, "candidate.xml", syscall.O_CREAT|syscall.O_EXCL|syscall.O_RDWR, true)
	if err != nil {
		return fmt.Errorf("cannot create private verification candidate")
	}
	w.candidate = f
	if n, writeErr := f.Write(data); writeErr != nil || n != len(data) {
		return fmt.Errorf("cannot stage keybox for verification")
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("cannot sync verification candidate")
	}
	w.staged, err = f.Stat()
	if err != nil {
		return fmt.Errorf("cannot inspect verification candidate")
	}
	return w.check(data)
}

func (w *filesWork) check(data []byte) error {
	if err := w.checkDir(); err != nil {
		return err
	}
	raw, st, err := filesRead(w.dir, "candidate.xml", filesMaxBytes, true)
	if err != nil || !filesSame(w.staged, st) || !bytes.Equal(raw, data) {
		return fmt.Errorf("verification candidate changed")
	}
	pinned, err := w.candidate.Stat()
	if err != nil || !filesSame(w.staged, pinned) {
		return fmt.Errorf("verification candidate changed")
	}
	return w.checkDir()
}

func filesDescriptorPath(f *os.File) string {
	return filepath.Join("/proc/self/fd", strconv.Itoa(int(f.Fd())))
}

func (w *filesWork) close() {
	if w.candidate != nil {
		w.candidate.Close()
	}
	// Remove contents through the original directory descriptor, never through
	// a replacement at w.path. RemoveAll does not follow child symlinks.
	names, _ := filesNames(w.dir)
	for _, name := range names {
		os.RemoveAll(filepath.Join(filesDescriptorPath(w.dir), name))
	}
	// A failed hook can rename the original directory. Find its inode only
	// within the private parent; do not remove a replacement directory.
	names, _ = filesNames(w.parent)
	for _, name := range names {
		if !strings.HasPrefix(name, "work.") {
			continue
		}
		fd, err := syscall.Openat(int(w.parent.Fd()), name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err != nil {
			continue
		}
		f := os.NewFile(uintptr(fd), name)
		st, err := f.Stat()
		f.Close()
		if err == nil && w.info != nil && os.SameFile(w.info, st) {
			os.Remove(filepath.Join(filesDescriptorPath(w.parent), name))
		}
	}
	// The allocated random name may now contain an attacker replacement;
	// remove that entry without following its links, still anchored to parent.
	os.RemoveAll(filepath.Join(filesDescriptorPath(w.parent), filepath.Base(w.path)))
	w.dir.Close()
	w.parent.Close()
}

func (e *engine) filesExpireWork(s *filesStore, name string) error {
	fd, err := syscall.Openat(int(s.dir.Fd()), name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil // Links and non-directories are never followed.
	}
	f := os.NewFile(uintptr(fd), name)
	st, err := f.Stat()
	f.Close()
	if err != nil {
		return fmt.Errorf("cannot inspect verification directory age")
	}
	if e.d.now().Sub(st.ModTime()) >= filesTTL {
		if err := os.RemoveAll(filepath.Join(filesDescriptorPath(s.dir), name)); err != nil {
			return fmt.Errorf("cannot remove expired verification directory")
		}
	}
	return nil
}

func (e *engine) filesRefreshSession(s *filesStore, id string) error {
	f, err := filesOpen(s.dir, id, syscall.O_RDONLY, true)
	if err != nil {
		return err
	}
	defer f.Close()
	// The full action budget is shorter than the renewed idle TTL. Other
	// commands may expire old sessions while verification releases files.lock.
	now := syscall.NsecToTimeval(e.d.now().UnixNano())
	if err = syscall.Futimes(int(f.Fd()), []syscall.Timeval{now, now}); err != nil {
		return fmt.Errorf("cannot renew active import session")
	}
	return nil
}
