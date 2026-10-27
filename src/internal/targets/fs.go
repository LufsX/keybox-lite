package targets

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

type disk struct {
	root         *os.File
	beforeCommit func(string, snapshot) error
}

func openDisk(root string) (*disk, error) {
	if root == "" {
		root = "/"
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(absolute, "/"), "/") {
		if part == "" {
			continue
		}
		next, e := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		syscall.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return &disk{root: os.NewFile(uintptr(fd), absolute)}, nil
}
func cleanDevice(p string) bool {
	return strings.HasPrefix(p, "/") && path.Clean(p) == p && p != "/" && !strings.ContainsAny(p, "\\\x00")
}
func (d *disk) parent(p string, create bool, mode uint32) (*os.File, string, error) {
	if !cleanDevice(p) {
		return nil, "", fmt.Errorf("unsafe path %q", p)
	}
	fd, err := syscall.Dup(int(d.root.Fd()))
	if err != nil {
		return nil, "", err
	}
	syscall.CloseOnExec(fd)
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	current := ""
	for _, part := range parts[:len(parts)-1] {
		current += "/" + part
		mode = 0755
		if current == stateDir || strings.HasPrefix(current, stateDir+"/") {
			mode = 0700
		}
		next, e := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if errors.Is(e, syscall.ENOENT) && create {
			e = syscall.Mkdirat(fd, part, mode)
			if e == nil || errors.Is(e, syscall.EEXIST) {
				next, e = syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
			}
		}
		syscall.Close(fd)
		if e != nil {
			return nil, "", e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), path.Dir(p)), parts[len(parts)-1], nil
}
func fdPath(f *os.File, n string) string { return fmt.Sprintf("/proc/self/fd/%d/%s", f.Fd(), n) }

type snapshot struct {
	Exists bool   `json:"exists"`
	Data   []byte `json:"data,omitempty"`
	Link   string `json:"link,omitempty"`
	Mode   uint32 `json:"mode"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
}

func (d *disk) read(p string, links bool) (snapshot, error) {
	var s snapshot
	dir, n, err := d.parent(p, false, 0)
	if errors.Is(err, syscall.ENOENT) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	defer dir.Close()
	st, err := os.Lstat(fdPath(dir, n))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	s.Exists = true
	s.Mode = uint32(st.Mode().Perm())
	if stat, ok := st.Sys().(*syscall.Stat_t); ok {
		s.UID = int(stat.Uid)
		s.GID = int(stat.Gid)
	}
	if st.Mode()&os.ModeSymlink != 0 {
		if !links {
			return s, fmt.Errorf("symlink refused: %s", p)
		}
		s.Link, err = os.Readlink(fdPath(dir, n))
		return s, err
	}
	if !st.Mode().IsRegular() {
		return s, fmt.Errorf("not a regular file: %s", p)
	}
	fd, err := syscall.Openat(int(dir.Fd()), n, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return s, err
	}
	f := os.NewFile(uintptr(fd), p)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return s, fmt.Errorf("not a regular file: %s", p)
	}
	stat := info.Sys().(*syscall.Stat_t)
	s.Mode = uint32(info.Mode().Perm())
	s.UID = int(stat.Uid)
	s.GID = int(stat.Gid)
	s.Data, err = io.ReadAll(io.LimitReader(f, maxIO+1))
	if len(s.Data) > maxIO {
		return s, fmt.Errorf("file exceeds IO limit: %s", p)
	}
	return s, err
}
func same(a, b snapshot) bool {
	return a.Exists == b.Exists && a.Link == b.Link && a.Mode == b.Mode && a.UID == b.UID && a.GID == b.GID && bytes.Equal(a.Data, b.Data)
}
func fileImage(data []byte, old snapshot, private bool) snapshot {
	s := snapshot{Exists: true, Data: data, Mode: 0644, UID: os.Geteuid(), GID: os.Getegid()}
	if old.Exists {
		s.Mode = old.Mode
		s.UID = old.UID
		s.GID = old.GID
	}
	if private {
		s.Mode = 0600
	}
	return s
}

type prepared struct {
	disk             *disk
	dir              *os.File
	name, temp, path string
	want             snapshot
}

func (p *prepared) close() {
	if p.temp != "" {
		syscall.Unlinkat(int(p.dir.Fd()), p.temp)
	}
	p.dir.Close()
}
func (p *prepared) commit() error {
	if p.disk.beforeCommit != nil {
		if err := p.disk.beforeCommit(p.path, p.want); err != nil {
			return err
		}
	}
	if !p.want.Exists {
		err := syscall.Unlinkat(int(p.dir.Fd()), p.name)
		if errors.Is(err, syscall.ENOENT) {
			return nil
		}
		if err != nil {
			return err
		}
	} else {
		if err := syscall.Renameat(int(p.dir.Fd()), p.temp, int(p.dir.Fd()), p.name); err != nil {
			return err
		}
		p.temp = ""
	}
	return p.dir.Sync()
}
func (d *disk) prepare(p string, want snapshot) (out *prepared, err error) {
	dir, n, err := d.parent(p, true, 0755)
	if err != nil {
		return nil, err
	}
	out = &prepared{disk: d, dir: dir, name: n, path: p, want: want}
	staged := out
	defer func() {
		if err != nil {
			staged.close()
		}
	}()
	if !want.Exists {
		return out, nil
	}
	random := make([]byte, 12)
	if _, err = rand.Read(random); err != nil {
		return nil, err
	}
	out.temp = ".targets-" + hex.EncodeToString(random)
	if want.Link != "" {
		err = os.Symlink(want.Link, fdPath(dir, out.temp))
		return out, err
	}
	fd, err := syscall.Openat(int(dir.Fd()), out.temp, syscall.O_CREAT|syscall.O_EXCL|syscall.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return out, err
	}
	f := os.NewFile(uintptr(fd), out.temp)
	if err = f.Chown(want.UID, want.GID); err == nil {
		err = f.Chmod(os.FileMode(want.Mode))
	}
	if err == nil {
		_, err = f.Write(want.Data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	return out, err
}
func (d *disk) write(p string, want snapshot) error {
	staged, err := d.prepare(p, want)
	if err != nil {
		return err
	}
	defer staged.close()
	return staged.commit()
}
func (d *disk) lock() (*os.File, error) {
	dir, n, err := d.parent(stateDir+"/lock", true, 0700)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	if err = dir.Chmod(0700); err != nil {
		return nil, err
	}
	fd, err := syscall.Openat(int(dir.Fd()), n, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "targets lock")
	st, err := f.Stat()
	if err == nil && (!st.Mode().IsRegular() || st.Sys().(*syscall.Stat_t).Nlink != 1) {
		err = fmt.Errorf("invalid lock file")
	}
	if err == nil {
		err = f.Chmod(0600)
	}
	if err == nil {
		err = syscall.Flock(fd, syscall.LOCK_EX)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

type change struct {
	Path   string
	Before snapshot
	After  snapshot
}
type journal struct {
	Version int
	Changes []change
}

var backendIDs = []string{"tricky_store", "oh_my_keymint", "teesim"}

func allowedJournalPath(p string) bool {
	if p == statePath {
		return true
	}
	for _, n := range []string{"config.ini", "target.txt", "security_patch.txt"} {
		if p == "/data/adb/tricky_store/"+n {
			return true
		}
	}
	// Backends that keep their app list inside a daemon configuration file.
	if p == "/data/adb/teesim/config.json" || p == "/data/misc/keystore/omk/injector.toml" {
		return true
	}
	for _, id := range backendIDs {
		for _, n := range []string{"webroot", "action.sh"} {
			if p == "/data/adb/modules/"+id+"/"+n {
				return true
			}
		}
	}
	return false
}
func (d *disk) recover() error {
	s, err := d.read(stateDir+"/transaction.json", false)
	if err != nil || !s.Exists {
		return err
	}
	var j journal
	if err = json.Unmarshal(s.Data, &j); err != nil || j.Version != 1 || len(j.Changes) > 16 {
		return fmt.Errorf("invalid targets transaction journal")
	}
	for _, c := range j.Changes {
		if !allowedJournalPath(c.Path) {
			return fmt.Errorf("invalid journal path")
		}
	}
	return d.rollback(j.Changes)
}
func (d *disk) rollback(changes []change) error {
	// Roll back only our exact writes; never replace an intervening foreign file.
	for i := len(changes) - 1; i >= 0; i-- {
		c := changes[i]
		cur, e := d.read(c.Path, true)
		if e != nil {
			return e
		}
		if same(cur, c.Before) {
			continue
		}
		if !same(cur, c.After) {
			return fmt.Errorf("transaction recovery conflict: %s", c.Path)
		}
		if e = d.write(c.Path, c.Before); e != nil {
			return e
		}
	}
	return d.write(stateDir+"/transaction.json", snapshot{})
}
func (d *disk) transact(changes []change) error {
	filtered := []change{}
	for _, c := range changes {
		if !allowedJournalPath(c.Path) {
			return fmt.Errorf("invalid transaction path")
		}
		if !same(c.Before, c.After) {
			cur, err := d.read(c.Path, true)
			if err != nil {
				return err
			}
			if !same(cur, c.Before) {
				return fmt.Errorf("concurrent file change: %s", c.Path)
			}
			filtered = append(filtered, c)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	data, err := json.Marshal(journal{1, filtered})
	if err != nil {
		return err
	}
	if len(data) > maxIO {
		return fmt.Errorf("transaction exceeds IO limit")
	}
	staged := make([]*prepared, 0, len(filtered))
	defer func() {
		for _, p := range staged {
			p.close()
		}
	}()
	for _, c := range filtered {
		p, e := d.prepare(c.Path, c.After)
		if e != nil {
			return e
		}
		staged = append(staged, p)
	}
	if err = d.write(stateDir+"/transaction.json", fileImage(data, snapshot{}, true)); err != nil {
		return err
	}
	for i, c := range filtered {
		cur, e := d.read(c.Path, true)
		if e == nil && !same(cur, c.Before) {
			e = fmt.Errorf("concurrent file change: %s", c.Path)
		}
		if e == nil {
			e = staged[i].commit()
		}
		if e != nil {
			if rollback := d.rollback(filtered); rollback != nil {
				return fmt.Errorf("%v; rollback pending: %w", e, rollback)
			}
			return e
		}
	}
	// Removing the journal commits the entire operation.
	if err = d.write(stateDir+"/transaction.json", snapshot{}); err != nil {
		if rollback := d.rollback(filtered); rollback != nil {
			return fmt.Errorf("%v; rollback pending: %w", err, rollback)
		}
		return err
	}
	return nil
}
