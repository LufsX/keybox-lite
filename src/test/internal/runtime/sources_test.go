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
	setResponses(t, e, map[string]response{
		e.meow:                            {Body: meowEncode("MEOW")},
		e.addon:                           {Body: encoded},
		"https://gh-proxy.com/" + e.addon: {Body: encoded},
	})
	var mu sync.Mutex
	dirs := map[string]bool{}
	e.d.prepare = func(_ context.Context, _ string, b []byte, dir string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		dirs[dir] = true
		if len(dirs) == 1 {
			return nil, fmt.Errorf("incompatible")
		}
		return b, nil
	}
	if err := e.update(context.Background()); err != nil {
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
func waitSourceCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("source condition timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func sourceProcessStopped(pid string) bool {
	raw, err := os.ReadFile("/proc/" + pid + "/stat")
	return os.IsNotExist(err) || (err == nil && strings.Contains(string(raw), ") Z "))
}

func TestSourceRaceDownloadCompletionCancelsBeforeValidation(t *testing.T) {
	for _, mode := range []string{"prepare", "verify", "incompatible", "verify-2", "verify-3", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			e := fixture(t)
			e.sources = []string{"meow", "spectre"}
			e.accel = "https://one/"
			first := meowEncode("FIRST")
			responses := map[string]response{
				e.meow:             {Body: first, Gate: "winner-release"},
				e.accel + e.meow:   {Body: meowEncode("SECOND"), Child: true, Gate: "sibling-release"},
				e.catalog:          {Body: `{"entries":[{"source":"good","version":"1"}]}`},
				e.base + "/good/1": {Body: spectreEncode("FALLBACK")},
			}
			setResponses(t, e, responses)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			var pid string
			pidReady := make(chan struct{})
			var prepared, checked []string
			stopped := func() {
				t.Helper()
				<-pidReady
				waitSourceCondition(t, func() bool { return sourceProcessStopped(pid) })
			}
			e.d.prepare = func(ctx context.Context, _ string, b []byte, _ string) ([]byte, error) {
				prepared = append(prepared, string(b))
				if string(b) == "FIRST" && (mode == "prepare" || mode == "incompatible") {
					stopped()
					if ctx.Err() != nil {
						t.Error("winner canceled with its sibling")
					}
					if mode == "incompatible" {
						return nil, fmt.Errorf("incompatible")
					}
				}
				return b, nil
			}
			e.d.verify = func(ctx context.Context, file, _ string, _ io.Writer) int {
				b := read(t, file)
				checked = append(checked, b)
				if b != "FIRST" {
					return 0
				}
				stopped()
				if ctx.Err() != nil {
					t.Error("winner verification canceled with its sibling")
				}
				switch mode {
				case "verify-2":
					return 2
				case "verify-3":
					return 3
				case "revoked":
					return 1
				}
				return 0
			}
			done := make(chan error, 1)
			// Start the slower downloader first, then let the direct route finish.
			go func() { done <- e.update(ctx) }()
			waitSourceCondition(t, func() bool { return exists(filepath.Join(e.p.root, "child")) })
			pid = strings.TrimSpace(read(t, filepath.Join(e.p.root, "child")))
			close(pidReady)
			mustWrite(t, filepath.Join(e.p.root, "winner-release"), nil)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if !sourceProcessStopped(pid) {
				t.Fatal("returned before losing downloader stopped")
			}
			log := read(t, filepath.Join(e.p.tmp, "keybox-update.log"))
			if strings.Count(log, "[meow / one] [FETCH] Starting") != 1 {
				t.Fatal("canceled route started more than once")
			}
			if strings.Contains(log, "context canceled; trying another downloader") {
				t.Fatal("normal cancellation logged as a download failure")
			}
			want := "FIRST"
			switch mode {
			case "incompatible", "verify-2", "verify-3", "revoked":
				want = "FALLBACK"
			}
			if strings.Contains(strings.Join(prepared, ","), "SECOND") {
				t.Fatal("restarted a canceled route")
			}
			if got := read(t, e.host("/data/adb/tricky_store/keybox.xml")); got != want {
				t.Fatalf("installed %q, want %q (prepared %v, checked %v)", got, want, prepared, checked)
			}
			clean(t, e)
		})
	}
}

func TestSourceRaceCatalogCompletionDoesNotCancelKeyboxDownloads(t *testing.T) {
	e := fixture(t)
	e.sources = []string{"spectre"}
	e.accel = "https://one/"
	catalog := `{"entries":[{"source":"good","version":"1"}]}`
	setResponses(t, e, map[string]response{
		e.catalog:                    {Body: catalog},
		e.base + "/good/1":           {Body: spectreEncode("DIRECT"), Child: true, Gate: "never-release"},
		e.accel + e.catalog:          {Body: catalog, Gate: "catalog-release"},
		e.accel + e.base + "/good/1": {Body: spectreEncode("PROXY")},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- e.update(ctx) }()
	waitSourceCondition(t, func() bool { return exists(filepath.Join(e.p.root, "child")) })
	pid := strings.TrimSpace(read(t, filepath.Join(e.p.root, "child")))
	mustWrite(t, filepath.Join(e.p.root, "catalog-release"), nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := read(t, e.host("/data/adb/tricky_store/keybox.xml")); got != "PROXY" {
		t.Fatalf("installed %q after direct catalog completed first", got)
	}
	if !sourceProcessStopped(pid) {
		t.Fatal("losing keybox downloader survived")
	}
	clean(t, e)
}

func TestSourceRaceConfiguredOrderAndFallback(t *testing.T) {
	for _, first := range []string{"meow", "spectre"} {
		for _, succeeds := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/success=%t", first, succeeds), func(t *testing.T) {
				e := fixture(t)
				second := "spectre"
				if first == "spectre" {
					second = "meow"
				}
				var err error
				e.sources, err = parseSources(first + " " + second + " " + first)
				if err != nil {
					t.Fatal(err)
				}
				e.accel = "https://one/"
				payloads := map[string]string{first: "FIRST", second: "SECOND"}
				responses := map[string]response{}
				for _, j := range e.jobs() {
					if j.source == "meow" {
						responses[j.prefix+e.meow] = response{Body: meowEncode(payloads[j.source])}
					} else {
						responses[j.prefix+e.catalog] = response{Body: `{"entries":[{"source":"good","version":"1"}]}`}
						responses[j.prefix+e.base+"/good/1"] = response{Body: spectreEncode(payloads[j.source])}
					}
				}
				setResponses(t, e, responses)
				entered := make(chan struct{}, len(e.jobs()))
				release := make(chan struct{})
				e.d.verify = func(ctx context.Context, file, _ string, _ io.Writer) int {
					if read(t, file) != "FIRST" {
						return 0
					}
					entered <- struct{}{}
					select {
					case <-release:
						if succeeds {
							return 0
						}
						return 2
					case <-ctx.Done():
						return 2
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- e.update(ctx) }()
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("first source did not reach verification")
				}
				secondURL := e.catalog
				if second == "meow" {
					secondURL = e.meow
				}
				if strings.Contains(read(t, filepath.Join(e.p.root, "requests")), secondURL) {
					t.Fatal("second source started before first source completed")
				}
				close(release)
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				want := "SECOND"
				if succeeds {
					want = "FIRST"
					if strings.Contains(read(t, filepath.Join(e.p.root, "requests")), secondURL) {
						t.Fatal("second source started despite first-source success")
					}
				}
				if got := read(t, e.host("/data/adb/tricky_store/keybox.xml")); got != want {
					t.Fatalf("installed %q, want %q", got, want)
				}
			})
		}
	}
}

func TestSourceRaceWorkerLimitAndQueue(t *testing.T) {
	e := fixture(t)
	e.sources = []string{"meow", "spectre", "addon"}
	e.accel = "https://one/ https://two/ https://three/ https://four/ https://five/ https://six/"
	// Queue routes within the first source and block the initial six workers.
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
		responses[j.prefix+url] = response{Body: "invalid", Code: 22, Gate: "release"}
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
