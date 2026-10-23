package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (e *engine) service(ctx context.Context, worker bool) error {
	if err := e.init(); err != nil {
		return err
	}
	if !e.enabled() {
		return fmt.Errorf("module is disabled or being removed")
	}
	c, err := e.readConfig()
	if err != nil {
		return err
	}
	if !worker {
		// Start the scheduler before the initial PM operation. Target recovery
		// and its boot readiness/retries are independent of keybox scheduling.
		if c.schedule != "off" {
			if err = e.d.launch(e.p.bin, "service", e.p.module, "--worker"); err != nil {
				return err
			}
		}
		if err = e.bootOnce(ctx); err != nil {
			e.say("[WARN] Target maintenance: %v; continuing keybox schedule.", err)
		}
		if !e.enabled() {
			return nil
		}
		return e.d.launch(e.p.bin, "maintenance", e.p.module)
	}
	if c.schedule == "off" {
		return nil
	}
	f, err := lock(ctx, filepath.Join(e.p.state, "scheduler.lock"), 0)
	if err != nil {
		if err.Error() == "lock busy: scheduler.lock" {
			return nil
		}
		return err
	}
	defer f.Close()
	pidFile := filepath.Join(e.p.state, "scheduler.pid")
	if err = e.atomic(pidFile, []byte(fmt.Sprintf("%d %s\n", os.Getpid(), processToken(os.Getpid()))), 0600); err != nil {
		return err
	}
	defer os.Remove(pidFile)
	for e.enabled() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c, err = e.readConfig()
		if err != nil {
			return err
		}
		if c.schedule == "off" {
			return nil
		}
		if err = e.tick(ctx, c); err != nil {
			return err
		}
		timer := time.NewTimer(e.poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}
func (e *engine) tick(ctx context.Context, c config) error {
	interval := int64(86400)
	if c.schedule == "weekly" {
		interval = 604800
	}
	now := e.d.now().Unix()
	if c.last <= now && now-c.last < interval {
		return nil
	}
	var err error
	if e.d.update != nil {
		err = e.d.update(ctx)
	} else {
		err = e.command(ctx, e.p.tmp, nil, nil, "run-update", filepath.Join(e.p.module, "action.sh"), e.p.state)
	}
	if err != nil {
		return nil
	} // Failed updates retry at the next 30-minute check.
	f, err := lock(ctx, filepath.Join(e.p.state, "config.lock"), 10*time.Second)
	if err != nil {
		return err
	}
	defer f.Close()
	if !e.enabled() {
		return nil
	}
	c, err = e.readConfig()
	if err != nil {
		return err
	}
	c.last = e.d.now().Unix()
	return e.writeConfig(c)
}
func (e *engine) uninstall(ctx context.Context) error {
	if err := e.init(); err != nil {
		return err
	}
	if err := e.atomic(filepath.Join(e.p.state, "stopping"), nil, 0600); err != nil {
		return err
	}
	if err := e.stopScheduler(); err != nil {
		return err
	}
	if err := e.stopRecord("maintenance.pid"); err != nil {
		return err
	}
	// Do not unlink lock inodes: late writers must contend on the same locks.
	var locks []*os.File
	defer func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].Close()
		}
	}()
	for _, name := range []string{"action.lock", "scheduler.lock", "maintenance.lock", "config.lock"} {
		f, err := lock(ctx, filepath.Join(e.p.state, name), 24*time.Hour)
		if err != nil {
			return err
		}
		locks = append(locks, f)
	}
	if err := e.d.cleanup(e.p.module); err != nil {
		return err
	}
	for _, p := range []string{e.p.conf, filepath.Join(e.p.tmp, "keybox-update.log"), filepath.Join(e.p.tmp, "keybox-update.history")} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
