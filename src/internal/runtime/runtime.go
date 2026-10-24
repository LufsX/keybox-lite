// Package runtime implements the device update, settings and scheduler commands.
package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"keyboxlite/internal/backend"
	"keyboxlite/internal/targets"
)

type paths struct{ root, module, state, conf, tmp, bin string }
type deps struct {
	detect        func(string) (backend.Report, error)
	prepare       func(context.Context, string, []byte, string) ([]byte, error)
	verify        func(context.Context, string, string, io.Writer) int
	boot, cleanup func(string) error
	now           func() time.Time
	launch        func(string, ...string) error
	update        func(context.Context) error
	rename        func(string, string) error
	copy          func(string, *os.File) error
	ready         func(context.Context) bool
	signal        func(int, syscall.Signal) error
}
type engine struct {
	p                                  paths
	d                                  deps
	out                                io.Writer
	log                                *os.File
	mu                                 sync.Mutex
	sources                            []string
	accel, meow, addon, catalog, base  string
	tools                              []downloader
	poll                               time.Duration
	downloadBudget                     time.Duration
	maintenancePoll, maintenanceBudget time.Duration
	configErr                          error
}

func production(module string) *engine {
	tmp := os.Getenv("TMPDIR")
	if tmp == "" {
		tmp = "/data/local/tmp"
	}
	bin := filepath.Join(module, "bin/keyboxlite")
	if !executable(bin) {
		bin = filepath.Join(module, "keyboxlite")
	}
	if !executable(bin) {
		if p, err := exec.LookPath("keyboxlite"); err == nil {
			bin = p
		}
	}
	e := &engine{p: paths{root: "/", module: module, state: "/data/adb/keybox-lite", conf: "/data/adb/keybox-lite/config", tmp: tmp, bin: bin}, out: os.Stdout,
		accel: "https://c.etc.moe/", poll: 30 * time.Minute, downloadBudget: 60 * time.Second, maintenancePoll: 30 * time.Second, maintenanceBudget: 10 * time.Minute}
	if value, ok := os.LookupEnv("KBL_SOURCES"); ok {
		e.sources, e.configErr = parseSources(value)
	}
	if value, ok := os.LookupEnv("KB_ACCEL"); ok {
		e.accel = value
	}
	e.meow = envDefault("MEOW_URL", "https://github.com/MeowDump/Integrity-Box/releases/download/archive/Megatron")
	e.addon = envDefault("ADDON_URL", "https://raw.githubusercontent.com/KOWX712/Tricky-Addon-Update-Target-List/keybox/.extra")
	e.catalog = envDefault("SPECTRE_CATALOG", "https://rawbin.dpejoh.com/catalog")
	e.base = envDefault("SPECTRE_BASE", "https://rawbin.dpejoh.com/key")
	e.d = deps{detect: backend.Detect, boot: targets.Boot, cleanup: targets.Cleanup, now: time.Now, rename: os.Rename, launch: launch, copy: copyFile, ready: deviceReady, signal: syscall.Kill}
	e.d.prepare = e.prepare
	e.d.verify = e.verify
	return e
}
func envDefault(k, v string) string {
	if x := os.Getenv(k); x != "" {
		return x
	}
	return v
}
func executable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode()&0111 != 0
}
func launch(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer null.Close()
	cmd.Stdin = null
	cmd.Stdout = null
	cmd.Stderr = null
	if err = cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Run accepts the complete os.Args[1:]. Side-effect commands have no root override.
func Run(args []string) int {
	if len(args) < 2 {
		return 2
	}
	cmd, module := args[0], args[1]
	if module == "" || !filepath.IsAbs(module) || strings.Contains(module, "\x00") {
		return 2
	}
	switch cmd {
	case "update", "uninstall", "maintenance":
		if len(args) != 2 {
			return 2
		}
	case "settings":
		if len(args) != 3 && len(args) != 4 {
			return 2
		}
		switch args[2] {
		case "get":
			if len(args) != 3 {
				return 2
			}
		case "set":
			if len(args) != 4 || !validSchedule(args[3]) {
				return 2
			}
		case "source":
			if len(args) != 4 || !validSource(args[3]) {
				return 2
			}
		default:
			return 2
		}
	case "service":
		if len(args) > 3 || len(args) == 3 && args[2] != "--worker" {
			return 2
		}
	default:
		return 2
	}
	e := production(module)
	if cmd == "update" && e.configErr != nil {
		fmt.Fprintln(os.Stderr, e.configErr)
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer cancel()
	var err error
	switch cmd {
	case "update":
		err = e.update(ctx)
	case "settings":
		err = e.settings(ctx, args[2:])
	case "service":
		err = e.service(ctx, len(args) == 3)
	case "uninstall":
		err = e.uninstall(ctx)
	case "maintenance":
		err = e.maintenance(ctx)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}
	return 0
}
func (e *engine) host(p string) string { return filepath.Join(e.p.root, strings.TrimPrefix(p, "/")) }
func (e *engine) say(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	e.mu.Lock()
	defer e.mu.Unlock()
	fmt.Fprintln(e.out, line)
	if e.log != nil {
		fmt.Fprintf(e.log, "%s | %s\n", e.d.now().Format("2006-01-02 15:04:05"), line)
	}
}
func exists(p string) bool { _, err := os.Lstat(p); return !os.IsNotExist(err) }
func (e *engine) enabled() bool {
	st, err := os.Stat(e.p.module)
	return err == nil && st.IsDir() && !exists(filepath.Join(e.p.state, "stopping")) && !exists(filepath.Join(e.p.module, "disable")) && !exists(filepath.Join(e.p.module, "remove"))
}
func (e *engine) init() error {
	if err := safePath(e.p.state, true); err != nil {
		return err
	}
	if err := os.MkdirAll(e.p.state, 0700); err != nil {
		return err
	}
	return os.Chmod(e.p.state, 0700)
}
func safePath(p string, dir bool) error {
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	cur := "/"
	parts := strings.Split(strings.TrimPrefix(abs, "/"), "/")
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe symlink: %s", cur)
		}
		if (i < len(parts)-1 || dir) && !st.IsDir() {
			return fmt.Errorf("not a directory: %s", cur)
		}
		if i == len(parts)-1 && !dir && !st.Mode().IsRegular() {
			return fmt.Errorf("not a regular file: %s", cur)
		}
	}
	return nil
}
