package targets

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"keyboxlite/internal/backend"
)

func (m *manager) linkSource(name string) string {
	if name == "action.sh" {
		name = "webui.sh"
	}
	return m.module + "/" + name
}

// lstatEntry distinguishes a native directory or dangling foreign symlink without
// following either. Symlink destinations are device paths even under --root.
func (d *disk) lstatEntry(p string) (snapshot, bool, error) {
	dir, n, err := d.parent(p, false, 0)
	if os.IsNotExist(err) {
		return snapshot{}, false, nil
	}
	if err != nil {
		return snapshot{}, false, err
	}
	defer dir.Close()
	st, err := os.Lstat(fdPath(dir, n))
	if os.IsNotExist(err) {
		return snapshot{}, false, nil
	}
	if err != nil {
		return snapshot{}, false, err
	}
	s := snapshot{Exists: true, Mode: uint32(st.Mode().Perm())}
	stat := st.Sys().(*syscall.Stat_t)
	s.UID = int(stat.Uid)
	s.GID = int(stat.Gid)
	if st.Mode()&os.ModeSymlink != 0 {
		s.Link, err = os.Readlink(fdPath(dir, n))
		return s, false, err
	}
	return s, true, nil
}
func (m *manager) entryStatus(st state, t *backend.Target) (Entry, error) {
	e := Entry{Enabled: st.Entry}
	if t == nil {
		return e, nil
	}
	id := t.ModuleID
	e.ModuleID = &id
	s, native, err := m.disk.lstatEntry(t.ModuleDir + "/webroot")
	if err != nil {
		return e, err
	}
	e.Linked = s.Exists && !native && s.Link == m.linkSource("webroot")
	e.HasOwnWebUI = s.Exists && !e.Linked
	return e, nil
}
func (m *manager) hasMagisk() (bool, error) {
	// Check the directory by opening it as a parent, refusing all symlinks.
	dir, _, err := m.disk.parent("/data/adb/magisk/.probe", false, 0)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	dir.Close()
	return true, nil
}
func (m *manager) entryChanges(enabled bool, t *backend.Target) ([]change, error) {
	out := []change{}
	for _, id := range backendIDs {
		dir := "/data/adb/modules/" + id
		for _, name := range []string{"webroot", "action.sh"} {
			p := dir + "/" + name
			s, native, err := m.disk.lstatEntry(p)
			if err != nil {
				return nil, err
			}
			ours := s.Exists && !native && s.Link == m.linkSource(name)
			// Switching active backend also clears our links on previous backends.
			if ours && (!enabled || t == nil || id != t.ModuleID) {
				out = append(out, change{p, s, snapshot{}})
			}
		}
	}
	if !enabled {
		return out, nil
	}
	if t == nil || !t.Active {
		return nil, fmt.Errorf("no active backend for WebUI entry")
	}
	webPath := t.ModuleDir + "/webroot"
	s, native, err := m.disk.lstatEntry(webPath)
	if err != nil {
		return nil, err
	}
	if s.Exists && (native || s.Link != m.linkSource("webroot")) {
		return nil, fmt.Errorf("backend already has a native or foreign WebUI entry")
	}
	// A real source webroot must exist; don't install a dangling link.
	sourceDir, _, err := m.disk.parent(m.module+"/webroot/.probe", false, 0)
	if err != nil {
		return nil, fmt.Errorf("module webroot: %w", err)
	}
	sourceDir.Close()
	if !s.Exists {
		want := snapshot{Exists: true, Link: m.linkSource("webroot"), Mode: 0777, UID: os.Geteuid(), GID: os.Getegid()}
		out = append(out, change{webPath, s, want})
	}
	magisk, err := m.hasMagisk()
	if err != nil {
		return nil, err
	}
	if magisk {
		p := t.ModuleDir + "/action.sh"
		action, native, e := m.disk.lstatEntry(p)
		if e != nil {
			return nil, e
		}
		if !action.Exists {
			source, e := m.disk.read(m.linkSource("action.sh"), false)
			if e != nil {
				return nil, e
			}
			if !source.Exists {
				return nil, fmt.Errorf("module webui.sh is missing")
			}
			want := snapshot{Exists: true, Link: m.linkSource("action.sh"), Mode: 0777, UID: os.Geteuid(), GID: os.Getegid()}
			out = append(out, change{p, action, want})
		} else if native || action.Link != m.linkSource("action.sh") { /* Keep a backend's existing action. */
		}
	} else {
		p := t.ModuleDir + "/action.sh"
		action, native, e := m.disk.lstatEntry(p)
		if e != nil {
			return nil, e
		}
		if action.Exists && !native && action.Link == m.linkSource("action.sh") {
			out = append(out, change{p, action, snapshot{}})
		}
	}
	return out, nil
}
func (m *manager) entryLocked(enabled *bool) error {
	if enabled != nil && *enabled {
		if err := m.requireRunning(); err != nil {
			return err
		}
	}
	if enabled == nil {
		stop, err := m.stopped()
		if err != nil || stop {
			return err
		}
	}
	st, raw, err := m.loadState()
	if err != nil {
		return err
	}
	t, _, err := m.detect()
	if err != nil {
		return err
	}
	if enabled != nil {
		st.Entry = *enabled
	}
	changes, err := m.entryChanges(st.Entry, t)
	if err != nil {
		return err
	}
	sc, err := stateChange(st, raw)
	if err != nil {
		return err
	}
	return m.disk.transact(append(changes, sc))
}

// cleanup owns its recovery gate so conflicts caused by backend updates cannot
// prevent uninstall from detaching this module's remaining links.
func (m *manager) cleanup() error { return m.cleanupWithState(false) }
func (m *manager) cleanupWithState(requireState bool) error {
	lock, err := m.disk.lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	return m.cleanupLockedWithState(requireState)
}
func (m *manager) cleanupLocked() error { return m.cleanupLockedWithState(false) }
func (m *manager) cleanupLockedWithState(requireState bool) error {
	var errs []error
	// Restore a safely recoverable multi-file save before uninstall. If external
	// changes block recovery, retire the journal and preserve those foreign files.
	// A recovery error must never prevent the following per-link cleanup.
	if err := m.disk.recover(); err != nil {
		if retireErr := m.discardJournal(); retireErr != nil {
			errs = append(errs, fmt.Errorf("cleanup recovery: %v; %w", err, retireErr))
		}
	}
	// Cleanup uses direct atomic operations and never creates a new link journal.
	for _, id := range backendIDs {
		for _, name := range []string{"webroot", "action.sh"} {
			p := "/data/adb/modules/" + id + "/" + name
			if err := m.disk.removeOwnedLink(p, m.linkSource(name)); err != nil {
				errs = append(errs, fmt.Errorf("cleanup %s: %w", p, err))
			}
		}
	}
	// A damaged state file must not prevent unlinking; preserve it verbatim.
	if st, raw, e := m.loadState(); e == nil {
		st.Entry = false
		sc, e := stateChange(st, raw)
		if e == nil && !same(sc.Before, sc.After) {
			e = m.disk.write(sc.Path, sc.After)
		}
		if e != nil {
			errs = append(errs, fmt.Errorf("cleanup state: %w", e))
		}
	} else if requireState {
		errs = append(errs, fmt.Errorf("cannot persist disabled entry state: %w", e))
	}
	return errors.Join(errs...)
}
func (m *manager) discardJournal() error {
	p := stateDir + "/transaction.json"
	s, _, err := m.disk.lstatEntry(p)
	if err != nil {
		return fmt.Errorf("cleanup journal inspection: %w", err)
	}
	if !s.Exists {
		return nil
	}
	if err = m.disk.write(p, snapshot{}); err == nil {
		return nil
	}
	// If unlink failed, try replacing the journal with an empty transaction. Even
	// when its final removal must be retried, it can no longer restore old links.
	neutral := fileImage([]byte("{\"Version\":1,\"Changes\":[]}"), s, true)
	if replaceErr := m.disk.write(p, neutral); replaceErr != nil {
		return fmt.Errorf("cleanup cannot retire pending journal: %w", errors.Join(err, replaceErr))
	}
	return fmt.Errorf("cleanup retired pending journal but could not remove it: %w", err)
}
func (m *manager) entrySet(enabled bool) error {
	if !enabled {
		return m.cleanupWithState(true)
	}
	return m.locked(func() error { return m.entryLocked(&enabled) })
}
func (m *manager) entryApply() error { return m.locked(func() error { return m.entryLocked(nil) }) }
