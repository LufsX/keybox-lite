package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// The caller holds action.lock for the entire log lifetime. Renaming an opened
// inode replaces a public log symlink without ever writing through that link.
func (e *engine) filesStartLog() (func(), error) {
	dir, err := filesOpenDir(e.p.tmp)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return nil, fmt.Errorf("cannot create log identifier")
	}
	name := "keybox-update.log." + hex.EncodeToString(token[:])
	f, err := filesOpen(dir, name, syscall.O_CREAT|syscall.O_EXCL|syscall.O_WRONLY, true)
	if err != nil {
		return nil, err
	}
	if err = syscall.Renameat(int(dir.Fd()), name, int(dir.Fd()), "keybox-update.log"); err != nil {
		f.Close()
		filesUnlink(dir, name)
		return nil, fmt.Errorf("cannot publish update log")
	}
	e.mu.Lock()
	previous := e.log
	e.log = f
	e.mu.Unlock()
	return func() {
		e.mu.Lock()
		e.log = previous
		e.mu.Unlock()
		syncErr := f.Sync()
		closeErr := f.Close()
		if syncErr != nil || closeErr != nil {
			e.say("[WARN] Could not persist the complete keybox log")
		}
	}, nil
}

func (e *engine) filesHistory(source string, size int) error {
	dir, err := filesOpenDir(e.p.tmp)
	if err != nil {
		return err
	}
	old, _, err := filesRead(dir, "keybox-update.history", filesMaxBytes, false)
	dir.Close()
	if os.IsNotExist(err) {
		old = nil
	} else if err != nil {
		return fmt.Errorf("cannot read update history safely")
	}
	entry := fmt.Sprintf("%s | %s | %d bytes | installed\n", e.d.now().Format("2006-01-02 15:04:05"), source, size)
	return e.atomic(filepath.Join(e.p.tmp, "keybox-update.history"), append(old, entry...), 0600)
}
