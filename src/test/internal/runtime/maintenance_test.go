package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeMaintenanceRetries(t *testing.T) {
	e := fixture(t)
	e.out = io.Discard
	var ready, boots int
	e.d.ready = func(context.Context) bool { ready++; return ready > 1 }
	e.d.boot = func(string) error {
		boots++
		if boots == 1 {
			return fmt.Errorf("pm starting")
		}
		return nil
	}
	if err := e.maintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	if boots != 2 || ready != 3 {
		t.Fatalf("ready %d boot %d", ready, boots)
	}
	if exists(filepath.Join(e.p.state, "maintenance.pid")) {
		t.Fatal("maintenance PID leaked")
	}
}
func TestRuntimeMaintenanceOffRecovery(t *testing.T) {
	e := fixture(t)
	boots, maintenance, workers := 0, 0, 0
	e.d.boot = func(string) error {
		boots++
		if boots == 1 {
			return fmt.Errorf("boot pending")
		}
		return nil
	}
	e.d.launch = func(_ string, args ...string) error {
		if args[0] == "maintenance" {
			maintenance++
		} else {
			workers++
		}
		return nil
	}
	if err := e.service(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if boots != 1 || maintenance != 1 || workers != 0 {
		t.Fatalf("off initial boot/maintenance/worker %d/%d/%d", boots, maintenance, workers)
	}
	if err := e.maintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	if boots != 2 {
		t.Fatal("off schedule did not recover target")
	}
	if !strings.Contains(fmt.Sprint(e.out), "[WARN] Target maintenance: boot pending") {
		t.Fatal("first failure not reported")
	}
}
func TestRuntimeMaintenanceSingleton(t *testing.T) {
	e := fixture(t)
	e.out = io.Discard
	entered := make(chan struct{})
	release := make(chan struct{})
	var ready, boots atomic.Int32
	e.d.ready = func(ctx context.Context) bool {
		ready.Add(1)
		close(entered)
		select {
		case <-release:
			return true
		case <-ctx.Done():
			return false
		}
	}
	e.d.boot = func(string) error { boots.Add(1); return nil }
	done := make(chan error, 1)
	go func() { done <- e.maintenance(context.Background()) }()
	<-entered
	if err := e.maintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ready.Load() != 1 || boots.Load() != 0 {
		t.Fatal("duplicate maintenance entered")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if boots.Load() != 1 {
		t.Fatal("maintenance skipped Boot")
	}
}
func TestRuntimeMaintenanceCancellationAndBudget(t *testing.T) {
	for _, mode := range []string{"cancel", "budget", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			e := fixture(t)
			e.out = io.Discard
			e.maintenanceBudget = 20 * time.Millisecond
			entered := make(chan struct{}, 1)
			e.d.ready = func(context.Context) bool {
				select {
				case entered <- struct{}{}:
				default:
				}
				return false
			}
			e.d.boot = func(string) error { t.Error("Boot before readiness"); return nil }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- e.maintenance(ctx) }()
			<-entered
			if mode == "cancel" {
				cancel()
			}
			if mode == "disabled" {
				mustWrite(t, filepath.Join(e.p.module, "disable"), nil)
			}
			select {
			case err := <-done:
				if mode == "cancel" && err == nil {
					t.Fatal("cancellation lost")
				}
			case <-time.After(time.Second):
				t.Fatal("maintenance persisted")
			}
			if exists(filepath.Join(e.p.state, "maintenance.pid")) {
				t.Fatal("stale PID")
			}
		})
	}
}
func TestRuntimeUninstallStopsMaintenanceBeforeCleanup(t *testing.T) {
	e := fixture(t)
	e.out = io.Discard
	if err := os.MkdirAll(e.p.state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.p.state, "config.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	oldLocks := map[string]os.FileInfo{}
	for _, relative := range []string{"targets/lock", "keybox-files/files.lock", "status-cache/" + strings.Repeat("a", 64) + ".lock"} {
		path := filepath.Join(e.p.state, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		oldLocks[path], _ = os.Stat(path)
		if err := os.WriteFile(filepath.Join(filepath.Dir(path), "payload"), []byte("temporary data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var boots, cleanups atomic.Int32
	e.d.boot = func(string) error {
		boots.Add(1)
		close(entered)
		<-release
		// Model a PM operation that finishes while uninstall has started.
		return fmt.Errorf("late PM result")
	}
	maintenanceDone := make(chan error, 1)
	go func() { maintenanceDone <- e.maintenance(context.Background()) }()
	<-entered
	e.d.cleanup = func(string) error {
		if boots.Load() != 1 {
			t.Error("unexpected Boot count")
		}
		cleanups.Add(1)
		return nil
	}
	uninstallDone := make(chan error, 1)
	go func() { uninstallDone <- e.uninstall(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for !exists(filepath.Join(e.p.state, "stopping")) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if e.enabled() {
		t.Fatal("uninstall marker missing")
	}
	if cleanups.Load() != 0 {
		t.Fatal("Cleanup raced active PM writer")
	}
	close(release)
	if err := <-maintenanceDone; err != nil {
		t.Fatal(err)
	}
	if err := <-uninstallDone; err != nil {
		t.Fatal(err)
	}
	if cleanups.Load() != 1 || exists(filepath.Join(e.p.state, "maintenance.pid")) {
		t.Fatal("maintenance cleanup missing")
	}
	// A worker launched just before uninstall must also refuse all late writes.
	if err := e.maintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.bootOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if boots.Load() != 1 {
		t.Fatal("Boot wrote after Cleanup")
	}
	if _, err := os.Stat(filepath.Join(e.p.state, "maintenance.lock")); err != nil {
		t.Fatal("lock inode removed")
	}
	for path, before := range oldLocks {
		after, err := os.Stat(path)
		if err != nil || !os.SameFile(before, after) {
			t.Fatal("legacy lock inode removed", path, err)
		}
		if exists(filepath.Join(filepath.Dir(path), "payload")) {
			t.Fatal("private data survived uninstall")
		}
	}
	if store, err := e.filesStore(context.Background()); err == nil {
		store.close()
		t.Fatal("upload storage reopened after uninstall")
	}
}
