package verify

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"keyboxlite/internal/status"
)

const (
	downloadConcurrency = 4
	maxStatusBytes      = 8 << 20
	downloadTimeout     = 30 * time.Second
	liveTimeout         = 20 * time.Second
)

type downloader struct {
	path string
	args []string
}

type statusDownload struct {
	raw    []byte
	url    string
	stale  bool
	cached bool
}

func busybox() string {
	for _, p := range []string{
		"/data/adb/ksu/bin/busybox", "/data/adb/ap/bin/busybox",
		"/data/adb/magisk/busybox", "/system/bin/busybox", "/system/xbin/busybox",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("busybox"); err == nil {
		return p
	}
	return ""
}

// /proc/self/fd/1 directs file-output tools into the bounded stdout pipe on
// Android/Linux, including wrappers that treat "-" as a literal file name.
func downloaders() []downloader {
	var out []downloader
	if p, err := exec.LookPath("curl"); err == nil {
		out = append(out, downloader{p, []string{"-fsSL", "--connect-timeout", "8",
			"--max-time", "20", "--speed-time", "15", "--speed-limit", "128",
			"-H", "Accept-Encoding: identity", "-o", "/proc/self/fd/1"}})
	}
	if p := busybox(); p != "" {
		out = append(out, downloader{p, []string{"wget", "-q", "-T", "20",
			"--header", "Accept-Encoding: identity", "-O", "/proc/self/fd/1"}})
	}
	if p, err := exec.LookPath("wget"); err == nil {
		out = append(out, downloader{p, []string{"-q", "-T", "20",
			"--header", "Accept-Encoding: identity", "-O", "/proc/self/fd/1"}})
	}
	return out
}

func statusURLs(accel, target string) []string {
	urls := []string{target}
	seen := map[string]bool{target: true}
	for _, prefix := range strings.Fields(accel) {
		u := prefix + target
		if !seen[u] {
			urls = append(urls, u)
			seen[u] = true
		}
	}
	if target == mirror && !seen["https://gh-proxy.com/"+mirror] {
		urls = append(urls, "https://gh-proxy.com/"+mirror)
	}
	return urls
}

// cappedOutput cancels the child as soon as its output exceeds the disk limit.
type cappedOutput struct {
	file      io.Writer
	remaining int64
	cancel    context.CancelFunc
}

func (w *cappedOutput) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		w.cancel()
		return 0, fmt.Errorf("status response exceeds size limit")
	}
	n, err := w.file.Write(p)
	w.remaining -= int64(n)
	return n, err
}

func downloadStatus(ctx context.Context, tool downloader, url, out string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	f, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer os.Remove(out)
	args := append(append([]string(nil), tool.args...), url)
	cmd := exec.CommandContext(ctx, tool.path, args...)
	// Inherit updatejob's process group; never detach with setsid/Setpgid.
	cmd.Stdout = &cappedOutput{file: f, remaining: limit, cancel: cancel}
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		return nil, err
	}
	if _, err := status.Check(raw, nil); err != nil {
		return nil, err
	}
	return raw, nil
}

// raceStatus waits for every worker, including canceled children, before returning.
func raceStatus(ctx context.Context, dir string, urls []string, tools []downloader, limit int64) (statusDownload, bool) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type job struct {
		tool downloader
		url  string
	}
	var jobs []job
	for _, tool := range tools {
		for _, url := range urls {
			jobs = append(jobs, job{tool, url})
		}
	}
	queue := make(chan int, len(jobs))
	for i := range jobs {
		queue <- i
	}
	close(queue)
	results := make(chan statusDownload, 1)
	var workers sync.WaitGroup
	for n := 0; n < downloadConcurrency && n < len(jobs); n++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range queue {
				if ctx.Err() != nil {
					return
				}
				j := jobs[i]
				raw, err := downloadStatus(ctx, j.tool, j.url, filepath.Join(dir, fmt.Sprintf("status-%d", i)), limit)
				if err != nil {
					continue
				}
				select {
				case results <- statusDownload{raw: raw, url: j.url}:
					cancel()
				default:
				}
				return
			}
		}()
	}
	workers.Wait()
	select {
	case result := <-results:
		return result, true
	default:
		return statusDownload{}, false
	}
}

func fetchStatusUncached(ctx context.Context, dir, accel string) (statusDownload, bool) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	tools := downloaders()
	liveCtx, liveCancel := context.WithTimeout(ctx, liveTimeout)
	result, ok := raceStatus(liveCtx, dir, statusURLs(accel, live), tools, maxStatusBytes)
	liveCancel()
	if ok {
		return result, true
	}
	if ctx.Err() != nil {
		return statusDownload{}, false
	}
	result, ok = raceStatus(ctx, dir, statusURLs(accel, mirror), tools, maxStatusBytes)
	result.stale = true
	return result, ok
}
