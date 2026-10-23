package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"keyboxlite/internal/backend"
)

func (e *engine) target() (backend.Target, error) {
	report, err := e.d.detect(e.p.root)
	if err != nil {
		return backend.Target{}, err
	}
	if report.Selected == nil || !report.Selected.Active || report.Selected.KeyboxPath == "" {
		return backend.Target{}, fmt.Errorf("no active supported backend or invalid backend configuration")
	}
	return *report.Selected, nil
}
func (e *engine) checkTarget(want backend.Target) error {
	if !e.enabled() {
		return fmt.Errorf("module is disabled or being removed")
	}
	got, err := e.target()
	if err != nil {
		return err
	}
	if got.Backend != want.Backend || got.KeyboxPath != want.KeyboxPath {
		return fmt.Errorf("backend or keybox path changed during update; retry")
	}
	return nil
}

// Chmod only directories created by this call, so a restrictive root-manager
// umask cannot make a newly installed backend path inaccessible to its consumer.
func targetDirectories(dir string) error {
	if err := safePath(dir, true); err != nil {
		return err
	}
	if st, err := os.Stat(dir); err == nil {
		if !st.IsDir() {
			return fmt.Errorf("not a directory")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(dir)
	if parent == dir {
		return fmt.Errorf("cannot create target root")
	}
	if err := targetDirectories(parent); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0755); err != nil {
		if os.IsExist(err) {
			return safePath(dir, true)
		}
		return err
	}
	fd, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), dir)
	defer f.Close()
	return f.Chmod(0755)
}
func copyFile(from string, to *os.File) error {
	f, err := os.Open(from)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(to, f)
	return err
}
func metadata(from os.FileInfo, to *os.File) error {
	if st, ok := from.Sys().(*syscall.Stat_t); ok {
		current, err := to.Stat()
		if err != nil {
			return err
		}
		dst := current.Sys().(*syscall.Stat_t)
		if st.Uid != dst.Uid || st.Gid != dst.Gid {
			if err = to.Chown(int(st.Uid), int(st.Gid)); err != nil {
				return err
			}
		}
	}
	return to.Chmod(from.Mode())
}
func (e *engine) install(ctx context.Context, t backend.Target, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := e.checkTarget(t); err != nil {
		return err
	}
	target := e.host(t.KeyboxPath)
	if err := safePath(target, false); err != nil {
		return err
	}
	if err := targetDirectories(filepath.Dir(target)); err != nil {
		return err
	}
	staged, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".new.")
	if err != nil {
		return err
	}
	defer os.Remove(staged.Name())
	defer staged.Close()
	st, err := os.Stat(target)
	if err == nil {
		if err = metadata(st, staged); err != nil {
			return err
		}
		backup, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".bak.")
		if err != nil {
			return err
		}
		name := backup.Name()
		err = e.d.copy(target, backup)
		if err == nil {
			err = metadata(st, backup)
		}
		if err == nil {
			err = backup.Sync()
		}
		closeErr := backup.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			os.Remove(name)
			return fmt.Errorf("cannot back up target; old keybox retained: %w", err)
		}
		e.say("Backed up old keybox -> %s", name)
	} else if os.IsNotExist(err) {
		if err = staged.Chmod(0644); err != nil {
			return err
		}
	} else {
		return err
	}
	if _, err = staged.Write(data); err != nil {
		return err
	}
	if err = staged.Sync(); err != nil {
		return err
	}
	if err = staged.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = e.checkTarget(t); err != nil {
		return err
	}
	if err = safePath(target, false); err != nil {
		return err
	}
	if err = e.d.rename(staged.Name(), target); err != nil {
		return fmt.Errorf("cannot replace target; old keybox retained: %w", err)
	}
	e.say("[DONE] Keybox installed to %s.", t.KeyboxPath)
	return nil
}
func (e *engine) update(ctx context.Context) (resultErr error) {
	if err := e.init(); err != nil {
		return err
	}
	if !e.enabled() {
		return fmt.Errorf("module is disabled or being removed")
	}
	f, err := lock(ctx, filepath.Join(e.p.state, "action.lock"), 0)
	if err != nil {
		return fmt.Errorf("another keybox update is running: %w", err)
	}
	defer f.Close()
	if !e.enabled() {
		return fmt.Errorf("module is disabled or being removed")
	}
	if err = safePath(e.p.tmp, true); err != nil {
		return err
	}
	work, err := os.MkdirTemp(e.p.tmp, "keybox-lite.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	log, err := os.CreateTemp(e.p.tmp, "keybox-update.log.")
	if err != nil {
		return err
	}
	defer log.Close()
	defer os.Remove(log.Name())
	// Rename the opened inode; writes never follow a replaced public log symlink.
	if err = e.d.rename(log.Name(), filepath.Join(e.p.tmp, "keybox-update.log")); err != nil {
		return err
	}
	e.log = log
	defer func() {
		if resultErr != nil {
			e.say("[FAIL] %v", resultErr)
		}
		e.mu.Lock()
		e.log = nil
		e.mu.Unlock()
	}()
	version := "unknown"
	if raw, err := os.ReadFile(filepath.Join(e.p.module, "module.prop")); err == nil {
		for _, line := range splitLines(string(raw)) {
			if len(line) > 8 && line[:8] == "version=" {
				version = line[8:]
			}
		}
	}
	e.say("Keybox Lite %s", version)
	e.say("[CHECK] Checking installation")
	t, err := e.target()
	if err != nil {
		return err
	}
	e.say("Backend: %s", t.Backend)
	e.say("Target: %s", t.KeyboxPath)
	if e.tools == nil {
		e.tools = findDownloaders()
	}
	if len(e.tools) == 0 {
		return fmt.Errorf("no supported downloader")
	}
	e.say("[FETCH] Downloading and checking candidates")
	result, err := e.race(ctx, work, string(t.Backend))
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	e.say("[OK] [%s] Passed backend and certificate checks (%d bytes)", result.job.source, len(result.data))
	e.say("[INSTALL] Installing for %s", t.Backend)
	if err = e.install(ctx, t, result.data); err != nil {
		return err
	}
	history := filepath.Join(e.p.tmp, "keybox-update.history")
	var old []byte
	if safePath(history, false) == nil {
		old, _ = os.ReadFile(history)
	}
	entry := fmt.Sprintf("%s | %s | %d bytes | installed\n", e.d.now().Format("2006-01-02 15:04:05"), result.job.source, len(result.data))
	if err = e.atomic(history, append(old, entry...), 0600); err != nil {
		e.say("WARNING: installed, but cannot record history")
	}
	return nil
}
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	return append(lines, s[start:])
}
