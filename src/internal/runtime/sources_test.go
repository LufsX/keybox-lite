package runtime

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSourcesCandidateFallback(t *testing.T) {
	for _, rc := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(rc), func(t *testing.T) {
			e := fixture(t)
			e.sources = []string{"spectre"}
			e.accel = ""
			catalog := `{"entries":[{"source":"first","version":"1"},{"source":"second","version":"1"},{"source":"third","version":"1"}]}`
			setResponses(t, e, map[string]response{e.catalog: {Body: catalog}, e.base + "/first/1": {Body: spectreEncode("FIRST")}, e.base + "/second/1": {Body: spectreEncode("GOOD")}, e.base + "/third/1": {Body: spectreEncode("UNUSED")}})
			var checked []string
			e.d.verify = func(_ context.Context, file, _ string, out io.Writer) int {
				data := read(t, file)
				checked = append(checked, data)
				fmt.Fprintln(out, "RESULT: synthetic full output")
				if data == "FIRST" {
					return rc
				}
				return 0
			}
			err := e.update(context.Background())
			if rc == 1 {
				if err == nil {
					t.Fatal("revoked source installed")
				}
				unchanged(t, e)
				if len(checked) != 1 {
					t.Fatal("continued revoked source")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if strings.Join(checked, ",") != "FIRST,GOOD" {
					t.Fatalf("candidate order: %v", checked)
				}
				if !strings.Contains(read(t, filepath.Join(e.p.tmp, "keybox-update.log")), "[spectre / direct] RESULT: synthetic full output") {
					t.Fatal("verify log missing")
				}
			}
			if strings.Contains(read(t, filepath.Join(e.p.root, "requests")), "third") {
				t.Fatal("downloaded after acceptance")
			}
		})
	}
}
func TestSourcesDecodeAndCatalogFailures(t *testing.T) {
	for _, mode := range []string{"invalid-catalog", "decode", "all-prepare", "partial", "empty"} {
		t.Run(mode, func(t *testing.T) {
			e := fixture(t)
			e.sources = []string{"spectre"}
			r := map[string]response{e.catalog: {Body: `{"entries":[{"source":"first","version":"1"},{"source":"second","version":"1"}]}`}, e.base + "/first/1": {Body: "invalid"}, e.base + "/second/1": {Body: spectreEncode("GOOD")}}
			if mode == "invalid-catalog" {
				r[e.catalog] = response{Body: "null"}
			}
			if mode == "decode" {
				r[e.base+"/second/1"] = response{Body: "invalid"}
			}
			if mode == "all-prepare" {
				e.d.prepare = func(context.Context, string, []byte, string) ([]byte, error) { return nil, fmt.Errorf("incompatible") }
			}
			if mode == "partial" {
				r[e.catalog] = response{Body: "partial", Code: 22}
			}
			if mode == "empty" {
				r[e.catalog] = response{}
			}
			setResponses(t, e, r)
			if err := e.update(context.Background()); err == nil {
				t.Fatal("bad source installed")
			}
			unchanged(t, e)
		})
	}
}
func TestSourcesAddonExplicitAndIsolation(t *testing.T) {
	e := fixture(t)
	e.sources = []string{"meow", "addon"}
	encoded := hex.EncodeToString([]byte(base64.StdEncoding.EncodeToString([]byte("ADDON"))))
	setResponses(t, e, map[string]response{e.addon: {Body: encoded}, e.meow: {Body: meowEncode("MEOW")}})
	var mu sync.Mutex
	dirs := map[string]bool{}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	e.d.prepare = func(_ context.Context, _ string, b []byte, dir string) ([]byte, error) {
		mu.Lock()
		dirs[dir] = true
		mu.Unlock()
		entered <- struct{}{}
		<-release
		return b, nil
	}
	done := make(chan error, 1)
	go func() { done <- e.update(context.Background()) }()
	<-entered
	<-entered
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(dirs) < 2 {
		t.Fatal("workers shared TMPDIR")
	}
	for dir := range dirs {
		if !strings.HasPrefix(dir, e.p.tmp+"/keybox-lite.") {
			t.Fatal("TMPDIR escaped work")
		}
	}
	clean(t, e)
}
func TestSourcesDownloaderFallback(t *testing.T) {
	for _, winner := range []string{"busybox", "wget"} {
		t.Run(winner, func(t *testing.T) {
			e := fixture(t)
			setResponses(t, e, map[string]response{
				"curl " + e.meow:      {Body: "partial", Code: 22},
				winner + " " + e.meow: {Body: meowEncode("GOOD")},
			})
			if err := e.update(context.Background()); err != nil {
				t.Fatal(err)
			}
			log := read(t, filepath.Join(e.p.tmp, "keybox-update.log"))
			if !strings.Contains(log, "Downloading with "+winner) || !strings.Contains(log, "synthetic-downloader-error") {
				t.Fatal("fallback diagnostics lost")
			}
		})
	}
}
func TestSourceRaceRevocationDuringShutdown(t *testing.T) {
	for _, spectreDelay := range []int{0, 100} {
		t.Run(fmt.Sprint(spectreDelay), func(t *testing.T) {
			e := fixture(t)
			e.sources = []string{"meow", "spectre"}
			e.accel = "https://c.etc.moe/"
			setResponses(t, e, map[string]response{
				e.meow:             {Body: meowEncode("MEOW-GOOD")},
				e.accel + e.meow:   {Body: meowEncode("MEOW-REVOKED")},
				e.catalog:          {Body: `{"entries":[{"source":"good","version":"1"}]}`},
				e.base + "/good/1": {Body: spectreEncode("SPECTRE"), Delay: spectreDelay},
			})
			revokedEntered := make(chan struct{})
			var once sync.Once
			e.d.verify = func(ctx context.Context, file, _ string, _ io.Writer) int {
				b := read(t, file)
				if b == "MEOW-REVOKED" {
					once.Do(func() { close(revokedEntered) })
					<-ctx.Done()
					return 1
				}
				if b == "MEOW-GOOD" {
					select {
					case <-revokedEntered:
					case <-ctx.Done():
						return 2
					}
				}
				return 0
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := e.update(ctx); err != nil {
				t.Fatal(err)
			}
			if read(t, e.host("/data/adb/tricky_store/keybox.xml")) != "SPECTRE" {
				t.Fatal("revoked source won shutdown race")
			}
			if !strings.Contains(read(t, filepath.Join(e.p.tmp, "keybox-update.history")), "spectre") {
				t.Fatal("wrong source history")
			}
		})
	}
}
func TestSourceRaceWorkerLimitAndQueue(t *testing.T) {
	e := fixture(t)
	e.sources = []string{"meow", "spectre", "addon"}
	e.accel = "https://one/ https://two/"
	// Block the initial six routes so queued workers cannot hide an exceeded limit.
	setResponses(t, e, map[string]response{})
	e.d.prepare = func(context.Context, string, []byte, string) ([]byte, error) { return nil, fmt.Errorf("no") }
	var responses = map[string]response{}
	for _, j := range e.jobs() {
		url := e.meow
		if j.source == "addon" {
			url = e.addon
		}
		if j.source == "spectre" {
			url = e.catalog
		}
		responses[j.prefix+url] = response{Body: "invalid", Gate: "release"}
	}
	setResponses(t, e, responses)
	e.tools = e.tools[:1]
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- e.update(ctx) }()
	count := func() int {
		raw, _ := os.ReadFile(filepath.Join(e.p.root, "requests"))
		return len(strings.FieldsFunc(string(raw), func(r rune) bool { return r == '\n' }))
	}
	deadline := time.Now().Add(3 * time.Second)
	for count() < 6 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if count() != 6 {
		t.Fatalf("six workers did not start: %d", count())
	}
	time.Sleep(100 * time.Millisecond)
	if count() != 6 {
		t.Fatalf("worker limit exceeded: %d", count())
	}
	mustWrite(t, filepath.Join(e.p.root, "release"), nil)
	if err := <-done; err == nil {
		t.Fatal("invalid payload installed")
	}
	requests := strings.Split(strings.TrimSpace(read(t, filepath.Join(e.p.root, "requests"))), "\n")
	if len(requests) != len(e.jobs()) {
		t.Fatalf("queued routes skipped %d/%d", len(requests), len(e.jobs()))
	}
	unchanged(t, e)
}
func TestSourceRaceCancellationAndDescendants(t *testing.T) {
	e := fixture(t)
	setResponses(t, e, map[string]response{e.meow: {Body: meowEncode("GOOD"), Delay: 120000, Child: true}, "https://gh-proxy.com/" + e.meow: {Delay: 120000}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.update(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for !exists(filepath.Join(e.p.root, "child")) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !exists(filepath.Join(e.p.root, "child")) {
		cancel()
		t.Fatal("downloader did not start")
	}
	pid := strings.TrimSpace(read(t, filepath.Join(e.p.root, "child")))
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled attempt installed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation stalled")
	}
	if raw, err := os.ReadFile("/proc/" + pid + "/stat"); err == nil && !strings.Contains(string(raw), ") Z ") {
		t.Fatalf("descendant %s survived", pid)
	}
	unchanged(t, e)
	f, err := lock(context.Background(), filepath.Join(e.p.state, "action.lock"), 0)
	if err != nil {
		t.Fatal("action lock retained")
	}
	f.Close()
}
func TestSourceRaceCancelVerify(t *testing.T) {
	e := fixture(t)
	setResponses(t, e, map[string]response{e.meow: {Body: meowEncode("GOOD")}})
	entered := make(chan struct{})
	var once sync.Once
	e.d.verify = func(ctx context.Context, _ string, _ string, out io.Writer) int {
		fmt.Fprintln(out, "RESULT: checking")
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return 2
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.update(ctx) }()
	<-entered
	cancel()
	if <-done == nil {
		t.Fatal("cancelled verification installed")
	}
	unchanged(t, e)
}
