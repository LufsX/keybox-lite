package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Children inherit updatejob's process group. Freeze before walking /proc so a
// downloader cannot fork beyond cancellation, then reap the direct child.
func stopTree(pid int) {
	if syscall.Kill(pid, syscall.SIGSTOP) != nil {
		return
	}
	waitProcessState(pid, true)
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
	children := strings.Fields(string(raw))
	if err != nil {
		entries, _ := filepath.Glob("/proc/[0-9]*/stat")
		for _, entry := range entries {
			b, _ := os.ReadFile(entry)
			end := strings.LastIndex(string(b), ") ")
			if end < 0 {
				continue
			}
			f := strings.Fields(string(b)[end+2:])
			if len(f) > 1 && f[1] == strconv.Itoa(pid) {
				children = append(children, filepath.Base(filepath.Dir(entry)))
			}
		}
	}
	for _, s := range children {
		if child, err := strconv.Atoi(s); err == nil {
			stopTree(child)
		}
	}
	syscall.Kill(pid, syscall.SIGKILL)
	waitProcessState(pid, false)
}

// Signal delivery is asynchronous. Confirm STOP before walking children and
// confirm death before returning, including grandchildren that we cannot Wait.
func waitProcessState(pid int, stopping bool) {
	until := time.Now().Add(time.Second)
	for {
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return
		}
		end := strings.LastIndex(string(raw), ") ")
		if end < 0 || len(raw) <= end+2 {
			return
		}
		state := raw[end+2]
		if state == 'Z' || state == 'X' || stopping && (state == 'T' || state == 't') {
			return
		}
		if time.Now().After(until) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}
func runCommand(ctx context.Context, cmd *exec.Cmd) error { return runProcess(ctx, cmd, false) }
func runProcess(ctx context.Context, cmd *exec.Cmd, graceful bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// Collect an already completed status before killing. In particular, exit 1
		// from Verify must survive sibling route cancellation.
		select {
		case err := <-done:
			return err
		default:
		}
		if graceful {
			// Let updatejob terminate its managed process group and remove control
			// records before escalating to a frozen process-tree cleanup.
			syscall.Kill(cmd.Process.Pid, syscall.SIGTERM)
			select {
			case err := <-done:
				return err
			case <-time.After(3 * time.Second):
			}
		}
		stopTree(cmd.Process.Pid)
		err := <-done
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() >= 0 {
			return err
		}
		return ctx.Err()
	}
}

type bounded struct {
	w      io.Writer
	left   int
	cancel context.CancelFunc
}

func (b *bounded) Write(p []byte) (int, error) {
	if len(p) > b.left {
		b.cancel()
		return 0, fmt.Errorf("response exceeds size limit")
	}
	n, err := b.w.Write(p)
	b.left -= n
	return n, err
}

type limitedDiagnostic struct{ b []byte }

func (b *limitedDiagnostic) Write(p []byte) (int, error) {
	n := len(p)
	if len(b.b) < 1024 {
		left := 1024 - len(b.b)
		if len(p) > left {
			p = p[:left]
		}
		b.b = append(b.b, p...)
	}
	return n, nil
}

func (e *engine) command(ctx context.Context, dir string, in io.Reader, out io.Writer, args ...string) error {
	cmd := exec.Command(e.p.bin, args...)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Env = append(os.Environ(), "TMPDIR="+dir, "KB_STATUS_CACHE="+filepath.Join(e.p.state, "status-cache"), "KB_ACCEL="+e.accel)
	return runProcess(ctx, cmd, len(args) > 0 && args[0] == "run-update")
}
func (e *engine) prepare(ctx context.Context, kind string, data []byte, dir string) ([]byte, error) {
	var b strings.Builder
	cmd := exec.Command(e.p.bin, "prepare", kind)
	cmd.Stdin = strings.NewReader(string(data))
	cmd.Stdout = &b
	cmd.Env = append(os.Environ(), "TMPDIR="+dir)
	err := runCommand(ctx, cmd)
	return []byte(b.String()), err
}
func (e *engine) verify(ctx context.Context, file, dir string, out io.Writer) int {
	err := e.command(ctx, dir, nil, out, "verify", file)
	if err == nil {
		return 0
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() >= 0 {
		return exit.ExitCode()
	}
	return 2
}

type logWriter struct {
	e       *engine
	prefix  string
	pending string
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.pending += string(p)
	for {
		line, rest, ok := strings.Cut(w.pending, "\n")
		if !ok {
			break
		}
		w.e.say("%s%s", w.prefix, line)
		w.pending = rest
	}
	return len(p), nil
}
func (w *logWriter) flush() {
	if w.pending != "" {
		w.e.say("%s%s", w.prefix, w.pending)
		w.pending = ""
	}
}
