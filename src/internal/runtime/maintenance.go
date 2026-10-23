package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func deviceReady(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.Command("getprop", "sys.boot_completed")
	cmd.Stdout = &out
	if err := runCommand(ctx, cmd); err == nil && strings.TrimSpace(out.String()) == "1" {
		return true
	}
	if ctx.Err() != nil {
		return false
	}
	// A ready package manager is sufficient on systems without getprop.
	out.Reset()
	cmd = exec.Command("pm", "path", "android")
	cmd.Stdout = &out
	return runCommand(ctx, cmd) == nil && strings.HasPrefix(strings.TrimSpace(out.String()), "package:")
}
func (e *engine) bootOnce(ctx context.Context) error {
	f, err := lock(ctx, filepath.Join(e.p.state, "maintenance.lock"), 0)
	if err != nil {
		if err.Error() == "lock busy: maintenance.lock" {
			return nil
		}
		return err
	}
	defer f.Close()
	if !e.enabled() || ctx.Err() != nil {
		return nil
	}
	return e.d.boot(e.p.module)
}
func (e *engine) maintenance(ctx context.Context) error {
	if err := e.init(); err != nil {
		return err
	}
	if !e.enabled() {
		return nil
	}
	f, err := lock(ctx, filepath.Join(e.p.state, "maintenance.lock"), 0)
	if err != nil {
		if err.Error() == "lock busy: maintenance.lock" {
			return nil
		}
		return err
	}
	defer f.Close()
	if !e.enabled() {
		return nil
	}
	pidFile := filepath.Join(e.p.state, "maintenance.pid")
	if err = e.atomic(pidFile, []byte(fmt.Sprintf("%d %s\n", os.Getpid(), processToken(os.Getpid()))), 0600); err != nil {
		return err
	}
	defer os.Remove(pidFile)
	bounded, cancel := context.WithTimeout(ctx, e.maintenanceBudget)
	defer cancel()
	for e.enabled() {
		if bounded.Err() != nil {
			break
		}
		ready := e.d.ready(bounded)
		// An uninstall can begin during readiness or PM work. The maintenance lock
		// remains held until the worker exits; Cleanup waits on that same inode.
		if !e.enabled() || bounded.Err() != nil {
			break
		}
		if ready {
			if err = e.d.boot(e.p.module); err == nil {
				return nil
			}
			e.say("[WARN] Target maintenance: %v; retrying after boot readiness.", err)
		}
		timer := time.NewTimer(e.maintenancePoll)
		select {
		case <-bounded.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if bounded.Err() != nil && e.enabled() {
		e.say("[WARN] Target maintenance reached its retry limit.")
	}
	return nil
}
