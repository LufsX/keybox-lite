package runtime

import (
	"context"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestStandaloneMaintenanceUninstallSignal(t *testing.T) {
	e := fixture(t)
	e.p.bin = helperWrapper(t, e.p.root, "keyboxlite", "")
	e.d.signal = syscall.Kill
	cmd := exec.Command(e.p.bin, "runtime-maintenance", e.p.module)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	deadline := time.Now().Add(3 * time.Second)
	for !exists(filepath.Join(e.p.state, "maintenance.pid")) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !exists(filepath.Join(e.p.state, "maintenance.pid")) {
		t.Fatal("maintenance never published PID")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if err := e.uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("persistent maintenance worker after uninstall")
	}
	if exists(filepath.Join(e.p.state, "maintenance.pid")) || exists(filepath.Join(e.p.root, "unexpected-boot")) {
		t.Fatal("maintenance wrote after uninstall")
	}
}
