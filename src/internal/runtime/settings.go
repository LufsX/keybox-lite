package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type config struct {
	schedule string
	last     int64
	source   string
}

func validSchedule(s string) bool { return s == "off" || s == "daily" || s == "weekly" }
func (e *engine) readConfig() (config, error) {
	c := config{schedule: "off", source: "auto"}
	// Settings live inside the state directory; earlier versions kept a file
	// next to it, so the legacy path is read until the first save migrates it.
	path := e.p.conf
	if !exists(path) && exists(e.legacyConf()) {
		path = e.legacyConf()
	}
	if err := safePath(path, false); err != nil {
		return c, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if k == "SCHEDULE" && validSchedule(v) {
			c.schedule = v
		}
		if k == "SOURCE" && validSource(v) {
			c.source = v
		}
		if k == "LAST_RUN" && v != "" && strings.Trim(v, "0123456789") == "" {
			v = strings.TrimLeft(v, "0")
			if v == "" {
				v = "0"
			}
			if len(v) <= 10 {
				n, err := strconv.ParseInt(v, 10, 64)
				if err == nil {
					c.last = n
				}
			}
		}
	}
	return c, nil
}
func (e *engine) legacyConf() string {
	return filepath.Join(filepath.Dir(e.p.state), "keybox-lite.conf")
}
func (e *engine) atomic(path string, data []byte, mode os.FileMode) error {
	if err := safePath(path, false); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = safePath(path, false)
	}
	if err == nil {
		err = e.d.rename(f.Name(), path)
	}
	return err
}
func (e *engine) writeConfig(c config) error {
	if err := e.atomic(e.p.conf, []byte(fmt.Sprintf("SCHEDULE=%s\nLAST_RUN=%d\nSOURCE=%s\n", c.schedule, c.last, c.source)), 0600); err != nil {
		return err
	}
	// The new file is authoritative; a leftover legacy file is only read when
	// the new one is missing, so removing it is best effort.
	if legacy := e.legacyConf(); legacy != e.p.conf {
		os.Remove(legacy)
	}
	return nil
}
func lock(ctx context.Context, path string, wait time.Duration) (*os.File, error) {
	if err := safePath(path, false); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	var st syscall.Stat_t
	if err = syscall.Fstat(fd, &st); err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFREG {
		f.Close()
		return nil, fmt.Errorf("unsafe lock")
	}
	end := time.Now().Add(wait)
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return nil, err
		}
		if wait == 0 || time.Now().After(end) {
			f.Close()
			return nil, fmt.Errorf("lock busy: %s", filepath.Base(path))
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}
func (e *engine) settings(ctx context.Context, args []string) error {
	if err := e.init(); err != nil {
		return err
	}
	if args[0] == "get" {
		c, err := e.readConfig()
		if err == nil {
			fmt.Fprintf(e.out, "SCHEDULE=%s\nLAST_RUN=%d\nSOURCE=%s\n", c.schedule, c.last, c.source)
		}
		return err
	}
	if !e.enabled() {
		return fmt.Errorf("module is disabled or being removed")
	}
	f, err := lock(ctx, filepath.Join(e.p.state, "config.lock"), 10*time.Second)
	if err != nil {
		return err
	}
	if !e.enabled() {
		f.Close()
		return fmt.Errorf("module is disabled or being removed")
	}
	c, err := e.readConfig()
	if err == nil {
		if args[0] == "source" {
			c.source = args[1]
		} else {
			c.schedule = args[1]
		}
		err = e.writeConfig(c)
	}
	f.Close()
	if err != nil {
		return err
	}
	if args[0] == "source" {
		// The scheduler re-reads the config for every run, so no relaunch is needed.
		fmt.Fprintf(e.out, "SOURCE=%s\n", c.source)
		return nil
	}
	fmt.Fprintf(e.out, "SCHEDULE=%s\n", c.schedule)
	if c.schedule == "off" {
		return e.stopScheduler()
	}
	return e.d.launch(e.p.bin, "service", e.p.module)
}
func processToken(pid int) string {
	if pid <= 1 {
		return ""
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	end := strings.LastIndex(string(raw), ") ")
	if end < 0 {
		return ""
	}
	fields := strings.Fields(string(raw)[end+2:])
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}
func (e *engine) stopScheduler() error { return e.stopRecord("scheduler.pid") }
func (e *engine) stopRecord(name string) error {
	raw, err := os.ReadFile(filepath.Join(e.p.state, name))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// A truncated or corrupted record is stale: its writer can no longer be
	// identified, but it re-reads settings and stops on its own. Uninstall must
	// not abort before cleanup because of it.
	fields := strings.Fields(string(raw))
	if len(fields) != 2 {
		return nil
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return nil
	}
	if token := processToken(pid); token != "" && token == fields[1] {
		if err = e.d.signal(pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			return err
		}
	}
	return nil
}
