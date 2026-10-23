package runtime

import (
	"context"
	"keyboxlite/internal/updatejob"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStandaloneSupervisorCancellation(t *testing.T) {
	for _, mode := range []string{"scheduler", "cancel-api"} {
		t.Run(mode, func(t *testing.T) { testSupervisorCancellation(t, mode) })
	}
}
func testSupervisorCancellation(t *testing.T, mode string) {
	e := fixture(t)
	realCommands(t, e)
	t.Setenv("KB_ACCEL", "")
	t.Setenv("KBL_SOURCES", "meow")
	payload := meowEncode(syntheticKeybox(t))
	setResponses(t, e, map[string]response{e.meow: {Body: payload, Delay: 120000, Child: true}})
	// Only the harness maps a sysroot. Production action.sh is tested separately
	// with an inert capture binary and never redirects device side effects.
	harness := "#!/bin/sh\nexec '" + strings.ReplaceAll(e.p.bin, "'", "'\\''") + "' runtime-update '" + strings.ReplaceAll(e.p.module, "'", "'\\''") + "'\n"
	mustWrite(t, filepath.Join(e.p.module, "action.sh"), []byte(harness))
	e.init()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- e.command(ctx, e.p.tmp, nil, nil, "run-update", filepath.Join(e.p.module, "action.sh"), e.p.state)
	}()
	deadline := time.Now().Add(4 * time.Second)
	for !exists(filepath.Join(e.p.root, "child")) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !exists(filepath.Join(e.p.root, "child")) {
		cancel()
		t.Fatal("managed downloader did not start")
	}
	if !exists(filepath.Join(e.p.state, "update-job.json")) {
		t.Fatal("supervisor record missing")
	}
	pid := read(t, filepath.Join(e.p.root, "child"))
	if mode == "cancel-api" {
		if rc := updatejob.Cancel([]string{e.p.state}); rc != 0 {
			t.Fatalf("cancel-update returned %d", rc)
		}
	} else {
		cancel()
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled supervisor succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor cancellation stalled")
	}
	if exists(filepath.Join(e.p.state, "update-job.json")) {
		t.Fatal("supervisor control record leaked")
	}
	if raw, err := os.ReadFile("/proc/" + pid + "/stat"); err == nil && !strings.Contains(string(raw), ") Z ") {
		t.Fatal("managed descendant survived")
	}
	unchanged(t, e)
}
