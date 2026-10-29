package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeActionContention(t *testing.T) {
	e := fixture(t)
	// Only direct route so the entry signal has one producer.
	e.meow = "https://example.test/payload"
	setResponses(t, e, map[string]response{e.meow: {Body: meowEncode("GOOD")}})
	entered := make(chan struct{})
	release := make(chan struct{})
	e.d.verify = func(context.Context, string, string, io.Writer) int { close(entered); <-release; return 0 }
	done := make(chan error, 1)
	go func() { done <- e.update(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first action stalled")
	}
	second := &engine{p: e.p, d: e.d, out: io.Discard}
	before := read(t, filepath.Join(e.p.root, "requests"))
	if err := second.update(context.Background()); err == nil || !strings.Contains(err.Error(), "another keybox update") {
		t.Fatalf("second action accepted: %v", err)
	}
	if read(t, filepath.Join(e.p.root, "requests")) != before {
		t.Fatal("contending action downloaded")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestRuntimeNoExternalFlock(t *testing.T) {
	e := fixture(t)
	t.Setenv("PATH", e.p.root)
	setResponses(t, e, map[string]response{e.meow: {Body: meowEncode("GOOD")}})
	if err := e.update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.settings(context.Background(), []string{"set", "off"}); err != nil {
		t.Fatal(err)
	}
	if err := e.uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestRuntimeScheduleConcurrentSettings(t *testing.T) {
	e := fixture(t)
	e.out = io.Discard
	e.init()
	e.writeConfig(config{schedule: "daily", last: 0, source: "auto"})
	entered := make(chan struct{})
	release := make(chan struct{})
	e.d.update = func(context.Context) error { close(entered); <-release; return nil }
	done := make(chan error, 1)
	go func() { done <- e.tick(context.Background(), config{schedule: "daily", last: 0, source: "auto"}) }()
	<-entered
	if err := e.settings(context.Background(), []string{"set", "weekly"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	c, err := e.readConfig()
	if err != nil || c.schedule != "weekly" || c.last == 0 {
		t.Fatalf("completion lost concurrent setting: %+v %v", c, err)
	}
}
func TestRuntimeUninstallInterruptsUpdate(t *testing.T) {
	e := fixture(t)
	e.out = io.Discard
	e.init()
	e.writeConfig(config{schedule: "daily", last: 0, source: "auto"})
	e.meow = "https://example.test/payload"
	setResponses(t, e, map[string]response{e.meow: {Body: meowEncode("GOOD")}})
	entered := make(chan struct{})
	release := make(chan struct{})
	e.d.verify = func(context.Context, string, string, io.Writer) int { close(entered); <-release; return 0 }
	updateDone := make(chan error, 1)
	go func() { updateDone <- e.update(context.Background()) }()
	<-entered
	uninstallDone := make(chan error, 1)
	go func() { uninstallDone <- e.uninstall(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for !exists(filepath.Join(e.p.state, "stopping")) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if e.enabled() {
		t.Fatal("uninstall did not stop writers")
	}
	select {
	case <-uninstallDone:
		t.Fatal("uninstall bypassed active writer")
	default:
	}
	close(release)
	if err := <-updateDone; err == nil {
		t.Fatal("in-flight update installed after uninstall")
	}
	if err := <-uninstallDone; err != nil {
		t.Fatal(err)
	}
	if exists(e.p.conf) {
		t.Fatal("uninstall retained settings")
	}
	unchanged(t, e)
}
func TestRuntimeUninstallRemovesState(t *testing.T) {
	e := fixture(t)
	e.out = io.Discard
	if err := e.init(); err != nil {
		t.Fatal(err)
	}
	stale := []string{"status-cache", "scheduler.pid", "maintenance.pid", "targets/state.json", "targets/transaction.json"}
	oldLock := filepath.Join(e.p.state, "targets", "lock")
	mustWrite(t, oldLock, nil)
	before, _ := os.Stat(oldLock)
	for _, name := range stale {
		mustWrite(t, filepath.Join(e.p.state, name), []byte("stale"))
	}
	if err := e.uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, name := range stale {
		if exists(filepath.Join(e.p.state, name)) {
			t.Fatalf("uninstall left state/%s", name)
		}
	}
	after, err := os.Stat(oldLock)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("uninstall removed legacy target lock inode")
	}
	// The late-writer guard files must survive for the root manager.
	if !exists(filepath.Join(e.p.state, "stopping")) {
		t.Fatal("uninstall lost stopping marker")
	}
	for _, name := range []string{"action.lock", "scheduler.lock", "maintenance.lock", "config.lock"} {
		if !exists(filepath.Join(e.p.state, name)) {
			t.Fatalf("uninstall lost state/%s", name)
		}
	}
}
func TestRuntimeUnsafeTargetsAndAtomicMetadata(t *testing.T) {
	for _, mode := range []string{"symlink", "directory", "parent-symlink", "backend-change", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			e := fixture(t)
			target, _ := e.target()
			p := e.host(target.KeyboxPath)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "symlink":
				os.Remove(p)
				os.Symlink(filepath.Join(e.p.root, "victim"), p)
			case "directory":
				os.Remove(p)
				os.Mkdir(p, 0700)
			case "parent-symlink":
				os.RemoveAll(filepath.Dir(p))
				os.Symlink(e.p.tmp, filepath.Dir(p))
			case "backend-change":
				os.RemoveAll(e.host("/data/adb/modules/tricky_store"))
			case "cancelled":
				cancel()
			}
			if err := e.install(ctx, target, []byte("NEW")); err == nil {
				t.Fatal("unsafe installation succeeded")
			}
		})
	}
	e := fixture(t)
	target, _ := e.target()
	p := e.host(target.KeyboxPath)
	e.d.copy = func(_ string, out *os.File) error { out.Write([]byte("PARTIAL")); return fmt.Errorf("copy failed") }
	if err := e.install(context.Background(), target, []byte("NEW")); err == nil {
		t.Fatal("partial backup accepted")
	}
	if read(t, p) != "OLD" {
		t.Fatal("partial backup overwrote old target")
	}
	for _, pattern := range []string{p + ".bak.*", p + ".new.*"} {
		matches, _ := filepath.Glob(pattern)
		if len(matches) > 0 {
			t.Fatalf("failed installation leaked %v", matches)
		}
	}
}
