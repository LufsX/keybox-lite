package verify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The helper is a real child process, so cancellation tests exercise exec.Wait.
func TestDownloaderProcess(t *testing.T) {
	if os.Getenv("VERIFY_HELPER") != "1" {
		return
	}
	args := os.Args
	url := args[len(args)-1]
	if len(args) > 2 && args[len(args)-2] == "force-invalid" {
		fmt.Print(`{"entries":null}`)
		os.Exit(0)
	}
	marker := filepath.Join(os.Getenv("VERIFY_MARKERS"), url)
	if err := os.WriteFile(marker, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		os.Exit(2)
	}
	switch {
	case strings.HasPrefix(url, "slow"):
		time.Sleep(10 * time.Second)
	case url == "barrier":
		// The response cannot win until the slow downloader really started.
		for i := 0; i < 500; i++ {
			if _, err := os.Stat(filepath.Join(os.Getenv("VERIFY_MARKERS"), "slow")); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	case url == "oversize":
		fmt.Print(strings.Repeat("x", 8192))
		time.Sleep(10 * time.Second)
	case url == "invalid":
		fmt.Print(`{"entries":null}`)
		os.Exit(0)
	case url == "partial":
		fmt.Print(`{"entries":{}}`)
		os.Exit(1)
	case url == "group":
		fmt.Printf(`{"entries":{},"pgrp":%d}`, syscall.Getpgrp())
		os.Exit(0)
	}
	fmt.Print(`{"entries":{}}`)
	os.Exit(0)
}

func helperDownloader(t *testing.T) (downloader, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	markers := t.TempDir()
	t.Setenv("VERIFY_HELPER", "1")
	t.Setenv("VERIFY_MARKERS", markers)
	return downloader{exe, []string{"-test.run=^TestDownloaderProcess$", "--"}}, markers
}

func TestRaceStatusFastValidCancelsAndWaits(t *testing.T) {
	tool, markers := helperDownloader(t)
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	result, ok := raceStatus(ctx, dir, []string{"slow", "barrier"}, []downloader{tool}, 4096)
	if !ok || result.url != "barrier" {
		t.Fatalf("result = %+v, %v", result, ok)
	}
	if time.Since(start) >= 5*time.Second {
		t.Fatal("waited for losing downloader")
	}
	pidBytes, err := os.ReadFile(filepath.Join(markers, "slow"))
	if err != nil {
		t.Fatal("slow downloader was not started:", err)
	}
	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatalf("loser still exists after return: %v", err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("download files leaked: %v, %v", files, err)
	}
}

func TestRaceStatusRejectsInvalidAndFailedResponses(t *testing.T) {
	tool, _ := helperDownloader(t)
	for _, url := range []string{"invalid", "partial", "oversize"} {
		t.Run(url, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			start := time.Now()
			if result, ok := raceStatus(ctx, t.TempDir(), []string{url}, []downloader{tool}, 4096); ok {
				t.Fatalf("accepted %s: %+v", url, result)
			}
			if time.Since(start) >= 3*time.Second {
				t.Fatal("size limit did not promptly terminate downloader")
			}
		})
	}
	// An invalid success from one tool must not suppress another tool on that URL.
	bad := downloader{tool.path, []string{"-test.run=^TestDownloaderProcess$", "--", "force-invalid"}}
	if _, ok := raceStatus(context.Background(), t.TempDir(), []string{"valid"}, []downloader{bad, tool}, 4096); !ok {
		t.Fatal("invalid downloader suppressed valid fallback")
	}
}

func TestRaceStatusBoundAndDeadline(t *testing.T) {
	tool, markers := helperDownloader(t)
	var urls []string
	for i := 0; i < 12; i++ {
		urls = append(urls, fmt.Sprintf("slow%d", i))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, ok := raceStatus(ctx, t.TempDir(), urls, []downloader{tool}, 4096); ok {
		t.Fatal("accepted timed-out response")
	}
	started, err := os.ReadDir(markers)
	if err != nil || len(started) != downloadConcurrency {
		t.Fatalf("started %d children, want %d: %v", len(started), downloadConcurrency, err)
	}
}

func TestDownloaderInheritsProcessGroup(t *testing.T) {
	tool, _ := helperDownloader(t)
	result, ok := raceStatus(context.Background(), t.TempDir(), []string{"group"}, []downloader{tool}, 4096)
	if !ok || !strings.Contains(string(result.raw), fmt.Sprintf(`"pgrp":%d`, syscall.Getpgrp())) {
		t.Fatalf("downloader changed process group: %s, %v", result.raw, ok)
	}
}

func TestStatusURLsDeduplicate(t *testing.T) {
	urls := statusURLs("https://proxy/ https://proxy/", live)
	if len(urls) != 2 || urls[0] != live || urls[1] != "https://proxy/"+live {
		t.Fatalf("unexpected URLs: %v", urls)
	}
}
