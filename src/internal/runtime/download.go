package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const maxDownload = 1048576

type downloader struct {
	name, path string
	args       []string
}

func findDownloaders() []downloader {
	var tools []downloader
	if p, err := exec.LookPath("curl"); err == nil {
		tools = append(tools, downloader{"curl", p, []string{"-fsSL", "--connect-timeout", "8", "--max-time", "60", "--speed-time", "15", "--speed-limit", "128", "--max-filesize", "1048576", "-o", "/proc/self/fd/1"}})
	}
	bb := ""
	for _, p := range []string{"/data/adb/ksu/bin/busybox", "/data/adb/ap/bin/busybox", "/data/adb/magisk/busybox", "/system/bin/busybox", "/system/xbin/busybox"} {
		if executable(p) {
			bb = p
			break
		}
	}
	if bb == "" {
		bb, _ = exec.LookPath("busybox")
	}
	if bb != "" {
		tools = append(tools, downloader{"busybox", bb, []string{"wget", "-q", "-T", "20", "-O", "/proc/self/fd/1"}})
	}
	if p, err := exec.LookPath("wget"); err == nil {
		tools = append(tools, downloader{"wget", p, []string{"-q", "-T", "20", "-O", "/proc/self/fd/1"}})
	}
	return tools
}
func githubURL(url string) bool {
	return strings.HasPrefix(url, "https://github.com/") || strings.HasPrefix(url, "https://raw.githubusercontent.com/")
}
func (e *engine) download(ctx context.Context, j routeJob, url, dir string) ([]byte, error) {
	if j.prefix == "https://gh-proxy.com/" && !githubURL(url) {
		return nil, fmt.Errorf("gh-proxy requires GitHub URL")
	}
	url = j.prefix + url
	e.say("[%s / %s] [FETCH] %s", j.source, j.route, url)
	for _, tool := range e.tools {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		e.say("[%s / %s] [FETCH] Downloading with %s", j.source, j.route, tool.name)
		child, cancel := context.WithTimeout(ctx, e.downloadBudget)
		var body bytes.Buffer
		diagnostic := &limitedDiagnostic{}
		cmd := exec.Command(tool.path, append(append([]string{}, tool.args...), url)...)
		cmd.Env = append(os.Environ(), "TMPDIR="+dir)
		cmd.Stdout = &bounded{w: &body, left: maxDownload, cancel: cancel}
		cmd.Stderr = diagnostic
		err := runCommand(child, cmd)
		if err == nil {
			err = child.Err()
		}
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil && body.Len() > 0 {
			return body.Bytes(), nil
		}
		reason := "returned an empty response"
		if err != nil {
			reason = err.Error()
		}
		if exit, ok := err.(*exec.ExitError); ok {
			reason = fmt.Sprintf("exit %d", exit.ExitCode())
		}
		e.say("[%s / %s] [WARN] Download: %s %s; trying another downloader", j.source, j.route, tool.name, reason)
		for i, line := range strings.Split(string(diagnostic.b), "\n") {
			if i >= 3 {
				break
			}
			if line != "" {
				e.say("[%s / %s]   %s", j.source, j.route, line)
			}
		}
	}
	return nil, fmt.Errorf("all downloaders failed")
}
