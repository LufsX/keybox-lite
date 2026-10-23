package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"keyboxlite/internal/backend"
	"keyboxlite/internal/info"
	"keyboxlite/internal/keybox"
	"keyboxlite/internal/updatejob"
	"keyboxlite/internal/verify"
)

type response struct {
	Body            string
	Gate            string
	Code, Delay     int
	Oversize, Child bool
}

func TestRuntimeHelper(t *testing.T) {
	dir := os.Getenv("KBL_RUNTIME_HELPER")
	if dir == "" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	args = args[1:]
	if len(args) == 0 {
		os.Exit(90)
	}
	switch args[0] {
	case "curl", "wget", "busybox":
		url := args[len(args)-1]
		f, err := os.OpenFile(filepath.Join(dir, "requests"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(91)
		}
		fmt.Fprintln(f, args[0]+" "+url)
		f.Close()
		raw, _ := os.ReadFile(filepath.Join(dir, "responses"))
		var rs map[string]response
		json.Unmarshal(raw, &rs)
		r, ok := rs[args[0]+" "+url]
		if !ok {
			r, ok = rs[url]
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "synthetic-downloader-error")
			os.Exit(22)
		}
		if r.Child {
			child := exec.Command("sleep", "120")
			child.Start()
			os.WriteFile(filepath.Join(dir, "child"), []byte(strconv.Itoa(child.Process.Pid)), 0600)
		}
		if r.Gate != "" {
			for !exists(filepath.Join(dir, r.Gate)) {
				time.Sleep(5 * time.Millisecond)
			}
		}
		if r.Delay > 0 {
			time.Sleep(time.Duration(r.Delay) * time.Millisecond)
		}
		if r.Oversize {
			fmt.Print(strings.Repeat("x", maxDownload+1))
		} else {
			fmt.Print(r.Body)
		}
		if r.Code != 0 {
			fmt.Fprintln(os.Stderr, "synthetic-downloader-error")
		}
		os.Exit(r.Code)
	case "prepare":
		raw, _ := io.ReadAll(os.Stdin)
		if os.Getenv("KBL_HELPER_REAL") == "1" {
			data, err := keybox.Prepare(raw, args[1])
			if err != nil {
				os.Exit(1)
			}
			os.Stdout.Write(data)
		} else {
			os.Stdout.Write(raw)
		}
		os.Exit(0)
	case "mkdir-target":
		syscall.Umask(0077)
		if err := targetDirectories(args[1]); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case "run-update":
		os.Exit(updatejob.Run(args[1:]))
	case "runtime-maintenance":
		e := production(args[1])
		e.p.root = dir
		e.p.state = filepath.Join(dir, "data/adb/keybox-lite")
		e.p.conf = filepath.Join(dir, "data/adb/keybox-lite.conf")
		e.p.tmp = filepath.Join(dir, "tmp")
		e.maintenancePoll = 10 * time.Millisecond
		e.maintenanceBudget = 5 * time.Second
		e.d.ready = func(context.Context) bool { return false }
		e.d.boot = func(string) error { os.WriteFile(filepath.Join(dir, "unexpected-boot"), nil, 0600); return nil }
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
		_ = e.maintenance(ctx)
		cancel()
		os.Exit(0)
	case "runtime-update":
		e := production(args[1])
		e.p.root = dir
		e.p.state = filepath.Join(dir, "data/adb/keybox-lite")
		e.p.conf = filepath.Join(dir, "data/adb/keybox-lite.conf")
		e.p.tmp = filepath.Join(dir, "tmp")
		e.p.bin = filepath.Join(dir, "keyboxlite")
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
		err := e.update(ctx)
		cancel()
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case "info":
		os.Exit(info.Run(args[1:]))
	case "verify":
		if os.Getenv("KBL_HELPER_VERIFY_WAIT") == "1" {
			child := exec.Command("sleep", "120")
			child.Start()
			os.WriteFile(filepath.Join(dir, "verify-child"), []byte(strconv.Itoa(child.Process.Pid)), 0600)
			fmt.Fprintln(os.Stdout, "[CHECK] full synthetic verify stdout")
			fmt.Fprintln(os.Stderr, "[WARN] full synthetic verify stderr")
			time.Sleep(120 * time.Second)
		}
		os.Exit(verify.Run(args[1:]))
	}
	os.Exit(92)
}
func helperWrapper(t *testing.T, dir, name, command string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	data := "#!/bin/sh\nexec '" + strings.ReplaceAll(exe, "'", "'\\''") + "' -test.run=^TestRuntimeHelper$ -- " + command + " \"$@\"\n"
	mustWrite(t, path, []byte(data))
	if err = os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
func mustWrite(t *testing.T, p string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) *engine {
	t.Helper()
	root := t.TempDir()
	e := production(filepath.Join(root, "module"))
	e.p = paths{root: root, module: filepath.Join(root, "module"), state: filepath.Join(root, "data/adb/keybox-lite"), conf: filepath.Join(root, "data/adb/keybox-lite.conf"), tmp: filepath.Join(root, "tmp")}
	os.MkdirAll(e.p.module, 0700)
	os.MkdirAll(e.p.tmp, 0700)
	e.out = &bytes.Buffer{}
	e.accel = ""
	e.sources = []string{"meow"}
	e.poll = time.Millisecond
	e.d.ready = func(context.Context) bool { return true }
	e.d.signal = func(int, syscall.Signal) error { return nil }
	e.maintenancePoll = time.Millisecond
	e.maintenanceBudget = 100 * time.Millisecond
	e.d.boot = func(string) error { return nil }
	e.d.cleanup = func(string) error { return nil }
	e.d.launch = func(string, ...string) error { return nil }
	e.d.prepare = func(_ context.Context, _ string, b []byte, _ string) ([]byte, error) { return b, nil }
	e.d.verify = func(context.Context, string, string, io.Writer) int { return 0 }
	t.Setenv("KBL_RUNTIME_HELPER", root)
	e.tools = []downloader{{"curl", helperWrapper(t, root, "curl", "curl"), nil}, {"busybox", helperWrapper(t, root, "busybox", "busybox"), nil}, {"wget", helperWrapper(t, root, "wget", "wget"), nil}}
	setResponses(t, e, nil)
	mustWrite(t, filepath.Join(root, "data/adb/modules/tricky_store/module.prop"), []byte("versionCode=246\n"))
	target := e.host("/data/adb/tricky_store/keybox.xml")
	mustWrite(t, target, []byte("OLD"))
	return e
}
func setResponses(t *testing.T, e *engine, r map[string]response) {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(e.p.root, "responses"), raw)
}
func meowEncode(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = 'a' + (c-'a'+13)%26
		} else if c >= 'A' && c <= 'Z' {
			b[i] = 'A' + (c-'A'+13)%26
		}
	}
	b = []byte(hex.EncodeToString(b))
	for i := 0; i < 10; i++ {
		b = []byte(base64.StdEncoding.EncodeToString(b))
	}
	return string(b)
}
func spectreEncode(s string) string {
	b := []byte(base64.StdEncoding.EncodeToString([]byte(s)))
	std := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	shuffled := "1dgWnocayqxU3r6vA5lCIPYfHmkV08b4tz+KMsp2NQ9LRXihODwSj7BEFJ/ZuGTe"
	for i, c := range b {
		if n := strings.IndexByte(std, c); n >= 0 {
			b[i] = shuffled[n]
		}
	}
	return string(b)
}
func read(t *testing.T, p string) string {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func unchanged(t *testing.T, e *engine) {
	t.Helper()
	if got := read(t, e.host("/data/adb/tricky_store/keybox.xml")); got != "OLD" {
		t.Fatalf("old target changed: %q", got)
	}
	if exists(filepath.Join(e.p.tmp, "keybox-update.history")) {
		t.Fatal("failed attempt recorded installed")
	}
	clean(t, e)
}
func clean(t *testing.T, e *engine) {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(e.p.tmp, "keybox-lite.*"))
	if len(matches) > 0 {
		t.Fatalf("leaked work: %v", matches)
	}
}

func TestRuntimeFailurePreservesTarget(t *testing.T) {
	for _, mode := range []string{"partial", "empty", "oversize", "decode", "prepare", "verify1", "verify2", "verify3", "verify7", "missing", "disable", "changed", "rename", "backup"} {
		t.Run(mode, func(t *testing.T) {
			e := fixture(t)
			r := response{Body: meowEncode("GOOD")}
			switch mode {
			case "partial":
				r.Code = 22
			case "empty":
				r.Body = ""
			case "oversize":
				r.Oversize = true
			case "decode":
				r.Body = "invalid"
			case "prepare":
				e.d.prepare = func(context.Context, string, []byte, string) ([]byte, error) { return nil, fmt.Errorf("incompatible") }
				e.d.verify = func(context.Context, string, string, io.Writer) int {
					t.Error("prepare failure reached verify")
					return 0
				}
			case "verify1", "verify2", "verify3", "verify7":
				rc, _ := strconv.Atoi(mode[len(mode)-1:])
				e.d.verify = func(context.Context, string, string, io.Writer) int { return rc }
			case "missing":
				os.RemoveAll(filepath.Join(e.p.root, "data/adb/modules"))
				e.d.prepare = func(context.Context, string, []byte, string) ([]byte, error) {
					t.Error("missing backend downloaded")
					return nil, fmt.Errorf("bad")
				}
			case "disable":
				e.d.verify = func(context.Context, string, string, io.Writer) int {
					mustWrite(t, filepath.Join(e.p.module, "disable"), nil)
					return 0
				}
			case "changed":
				e.d.verify = func(context.Context, string, string, io.Writer) int {
					mustWrite(t, filepath.Join(e.p.root, "data/adb/modules/tricky_store/disable"), nil)
					return 0
				}
			case "rename":
				e.d.rename = func(a, b string) error {
					if strings.HasSuffix(b, "keybox.xml") {
						return fmt.Errorf("replace failure")
					}
					return os.Rename(a, b)
				}
			case "backup":
				e.d.copy = func(_ string, _ *os.File) error { return fmt.Errorf("injected backup failure") }
			}
			setResponses(t, e, map[string]response{e.meow: r, "https://gh-proxy.com/" + e.meow: r})
			if err := e.update(context.Background()); err == nil {
				t.Fatal("failure installed")
			}
			unchanged(t, e)
			if mode == "missing" && exists(filepath.Join(e.p.root, "requests")) {
				t.Fatal("missing backend started downloads")
			}
		})
	}
}
func TestRuntimeInstallMetadataHistory(t *testing.T) {
	e := fixture(t)
	target := e.host("/data/adb/tricky_store/keybox.xml")
	os.Chmod(target, 0600)
	setResponses(t, e, map[string]response{e.meow: {Body: meowEncode("GOOD")}})
	if err := e.update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if read(t, target) != "GOOD" {
		t.Fatal("new data missing")
	}
	st, _ := os.Stat(target)
	if st.Mode().Perm() != 0600 {
		t.Fatal("mode changed")
	}
	backups, _ := filepath.Glob(target + ".bak.*")
	if len(backups) != 1 || read(t, backups[0]) != "OLD" {
		t.Fatal("backup missing")
	}
	if !strings.Contains(read(t, filepath.Join(e.p.tmp, "keybox-update.history")), "meow | 4 bytes | installed") {
		t.Fatal("history contract")
	}
	clean(t, e)
	// New targets and nested parents use traversable permissions; existing mode remains.
	os.Remove(target)
	tgt, _ := e.target()
	tgt.KeyboxPath = "/data/adb/tricky_store/nested/new.xml"
	e.d.detect = func(string) (backend.Report, error) { return backend.Report{Selected: &tgt}, nil }
	if err := e.install(context.Background(), tgt, []byte("NEW")); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{e.host(tgt.KeyboxPath): 0644, e.host("/data/adb/tricky_store/nested"): 0755} {
		st, _ := os.Stat(p)
		if st.Mode().Perm() != want {
			t.Fatalf("%s mode %o", p, st.Mode().Perm())
		}
	}
}
func TestRuntimeConfiguration(t *testing.T) {
	t.Setenv("KBL_SOURCES", "meow spectre addon meow")
	t.Setenv("KB_ACCEL", "https://one/ https://two/")
	t.Setenv("KBL_TEST_ROOT", t.TempDir())
	e := production("/module")
	if e.p.root != "/" || e.p.state != "/data/adb/keybox-lite" || len(e.sources) != 3 || len(e.jobs()) != 11 {
		t.Fatalf("configuration: %+v jobs %d", e.p, len(e.jobs()))
	}
	for _, j := range e.jobs() {
		if j.source == "spectre" && j.prefix == "https://gh-proxy.com/" {
			t.Fatal("non-GitHub proxy")
		}
	}
	for _, value := range []string{"", "meow unknown", "addon;id"} {
		if _, err := parseSources(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	for _, args := range [][]string{{"update", "/module", "--root", "/tmp"}, {"settings", "/module", "set", "hourly"}, {"service", "/module", "bad"}} {
		if Run(args) != 2 {
			t.Fatal("accepted invalid CLI")
		}
	}
	t.Setenv("KBL_SOURCES", "meow")
	e = production("/module")
	if len(e.sources) != 1 || e.sources[0] != "meow" {
		t.Fatal("single source ignored")
	}
	t.Setenv("KB_ACCEL", "")
	t.Setenv("KBL_SOURCES", "")
	if production("/module").configErr == nil {
		t.Fatal("empty sources accepted")
	}
	t.Setenv("KBL_SOURCES", "meow spectre")
	os.Unsetenv("KB_ACCEL")
	e = production("/module")
	if e.accel != "https://c.etc.moe/" || len(e.jobs()) != 5 {
		t.Fatal("default routes changed")
	}
}
func TestRuntimeSettingsAtomic(t *testing.T) {
	e := fixture(t)
	e.init()
	mustWrite(t, e.p.conf, []byte("SCHEDULE=weekly\nLAST_RUN=0009\ntouch /unexecuted\n"))
	c, err := e.readConfig()
	if err != nil || c.schedule != "weekly" || c.last != 9 {
		t.Fatalf("%+v %v", c, err)
	}
	mustWrite(t, e.p.conf, []byte("SCHEDULE=bad\nLAST_RUN=999999999999999999999\n"))
	c, _ = e.readConfig()
	if c.schedule != "off" || c.last != 0 {
		t.Fatal("invalid config accepted")
	}
	old := read(t, e.p.conf)
	e.d.rename = func(string, string) error { return fmt.Errorf("rename failed") }
	if e.settings(context.Background(), []string{"set", "off"}) == nil {
		t.Fatal("save failure ignored")
	}
	if read(t, e.p.conf) != old {
		t.Fatal("failed write changed settings")
	}
	matches, _ := filepath.Glob(e.p.conf + ".*")
	if len(matches) != 0 {
		t.Fatal("temporary settings leaked")
	}
	e.d.rename = os.Rename
	os.Remove(e.p.conf)
	os.Symlink(filepath.Join(e.p.root, "other"), e.p.conf)
	if _, err = e.readConfig(); err == nil {
		t.Fatal("config symlink accepted")
	}
}
func TestRuntimeLocks(t *testing.T) {
	e := fixture(t)
	e.init()
	for _, name := range []string{"action.lock", "config.lock", "scheduler.lock"} {
		p := filepath.Join(e.p.state, name)
		f, err := lock(context.Background(), p, 0)
		if err != nil {
			t.Fatal(err)
		}
		if g, err := lock(context.Background(), p, 0); err == nil {
			g.Close()
			t.Fatal("contending lock acquired")
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			g, err := lock(ctx, p, time.Second)
			if g != nil {
				g.Close()
			}
			done <- err
		}()
		cancel()
		if <-done == nil {
			t.Fatal("cancelled wait acquired")
		}
		f.Close()
		g, err := lock(context.Background(), p, 0)
		if err != nil {
			t.Fatal(err)
		}
		g.Close()
		os.Remove(p)
		os.Symlink(filepath.Join(e.p.root, "victim"), p)
		if g, err := lock(context.Background(), p, 0); err == nil {
			g.Close()
			t.Fatal("symlink lock accepted")
		}
		os.Remove(p)
	}
}
func TestRuntimeSettingsWait(t *testing.T) {
	e := fixture(t)
	e.init()
	f, _ := lock(context.Background(), filepath.Join(e.p.state, "config.lock"), 0)
	done := make(chan error, 1)
	go func() { done <- e.settings(context.Background(), []string{"set", "off"}) }()
	select {
	case <-done:
		t.Fatal("settings skipped lock")
	case <-time.After(80 * time.Millisecond):
	}
	f.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestRuntimeScheduleAndUninstall(t *testing.T) {
	e := fixture(t)
	e.init()
	boot := 0
	e.d.boot = func(string) error { boot++; return nil }
	if err := e.service(context.Background(), false); err != nil || boot != 1 {
		t.Fatalf("off schedule skipped Boot: %v %d", err, boot)
	}
	e.writeConfig(config{"daily", 0})
	e.d.update = func(context.Context) error { return fmt.Errorf("failed") }
	if err := e.tick(context.Background(), config{"daily", 0}); err != nil {
		t.Fatal(err)
	}
	c, _ := e.readConfig()
	if c.last != 0 {
		t.Fatal("failure advanced timestamp")
	}
	e.d.update = func(context.Context) error { return e.writeConfig(config{"weekly", 0}) }
	if err := e.tick(context.Background(), config{"daily", 0}); err != nil {
		t.Fatal(err)
	}
	c, _ = e.readConfig()
	if c.schedule != "weekly" || c.last == 0 {
		t.Fatal("completion overwrote settings")
	}
	called := 0
	e.d.update = func(context.Context) error { called++; return nil }
	e.tick(context.Background(), c)
	if called != 0 {
		t.Fatal("not due schedule ran")
	}
	e.tick(context.Background(), config{"daily", e.d.now().Unix() + 100})
	if called != 1 {
		t.Fatal("future timestamp suppressed update")
	}
	cleaned := false
	e.d.cleanup = func(string) error { cleaned = true; return nil }
	if err := e.uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !cleaned || exists(e.p.conf) || !exists(filepath.Join(e.p.state, "stopping")) {
		t.Fatal("uninstall cleanup")
	}
	if err := e.update(context.Background()); err == nil {
		t.Fatal("update after uninstall")
	}
	if err := e.settings(context.Background(), []string{"set", "daily"}); err == nil {
		t.Fatal("late settings writer")
	}
	unchanged(t, e)
}
func TestRuntimeUninstallWaitsWriter(t *testing.T) {
	e := fixture(t)
	e.init()
	f, _ := lock(context.Background(), filepath.Join(e.p.state, "action.lock"), 0)
	done := make(chan error, 1)
	go func() { done <- e.uninstall(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for !exists(filepath.Join(e.p.state, "stopping")) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if e.enabled() {
		t.Fatal("uninstall did not block writers")
	}
	select {
	case <-done:
		t.Fatal("uninstall skipped writer lock")
	default:
	}
	f.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestRuntimeSchedulerSingleton(t *testing.T) {
	e := fixture(t)
	e.init()
	e.writeConfig(config{"daily", e.d.now().Unix()})
	f, _ := lock(context.Background(), filepath.Join(e.p.state, "scheduler.lock"), 0)
	if err := e.service(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	f.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.service(ctx, true) }()
	deadline := time.Now().Add(time.Second)
	for !exists(filepath.Join(e.p.state, "scheduler.pid")) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if exists(filepath.Join(e.p.state, "scheduler.pid")) {
		t.Fatal("scheduler record leaked")
	}
}
