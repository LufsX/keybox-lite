package runtime

// Source formats follow Duck-ToolBox, revision
// 7567a8e084d31b1a6da3541bf0e8e2692ee45e0a (Eltavine, MIT).

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"keyboxlite/internal/catalog"
	"keyboxlite/internal/decode"
)

type routeJob struct{ source, route, prefix string }
type sourceState struct {
	ctx     context.Context
	cancel  context.CancelFunc
	revoked atomic.Bool
	claim   func(routeJob) bool
}
type routeResult struct {
	job  routeJob
	data []byte
}

func (e *engine) jobs() []routeJob {
	var jobs []routeJob
	for _, source := range e.sources {
		jobs = append(jobs, routeJob{source, "direct", ""})
		seen := map[string]bool{"": true, "https://gh-proxy.com/": true}
		for _, prefix := range strings.Fields(e.accel) {
			if seen[prefix] {
				continue
			}
			seen[prefix] = true
			jobs = append(jobs, routeJob{source, strings.TrimSuffix(strings.TrimPrefix(prefix, "https://"), "/"), prefix})
		}
		if source != "spectre" || githubURL(e.catalog) {
			jobs = append(jobs, routeJob{source, "gh-proxy", "https://gh-proxy.com/"})
		}
	}
	return jobs
}
func (e *engine) candidate(ctx context.Context, j routeJob, s *sourceState, url, dir, kind string) ([]byte, error) {
	if s.revoked.Load() {
		return nil, fmt.Errorf("source revoked")
	}
	raw, err := e.download(ctx, j, url, dir)
	if err != nil {
		return nil, err
	}
	// Catalog downloads never claim the race. Stop sibling routes as soon as
	// a complete keybox response arrives, before decoding or validation.
	if ctx.Err() != nil || !s.claim(j) {
		return nil, fmt.Errorf("route stopped")
	}
	var decoded bytes.Buffer
	switch j.source {
	case "meow":
		err = decode.Meow(bytes.NewReader(raw), &decoded)
	case "spectre":
		err = decode.Spectre(bytes.NewReader(raw), &decoded)
	case "addon":
		err = decode.Addon(bytes.NewReader(raw), &decoded)
	default:
		return nil, fmt.Errorf("unknown source")
	}
	if err != nil || decoded.Len() == 0 {
		e.say("[%s / %s] [WARN] Decode failed", j.source, j.route)
		return nil, fmt.Errorf("decode failed")
	}
	prepared, err := e.d.prepare(ctx, kind, decoded.Bytes(), dir)
	if err != nil {
		e.say("[%s / %s] [WARN] Incompatible with %s; trying next candidate.", j.source, j.route, kind)
		return nil, err
	}
	if ctx.Err() != nil || s.revoked.Load() {
		return nil, fmt.Errorf("source stopped")
	}
	file := filepath.Join(dir, "candidate")
	if err = os.WriteFile(file, prepared, 0600); err != nil {
		return nil, err
	}
	e.say("[%s / %s] [CHECK] Checking certificates", j.source, j.route)
	output := &logWriter{e: e, prefix: fmt.Sprintf("[%s / %s] ", j.source, j.route)}
	rc := e.d.verify(ctx, file, dir, output)
	output.flush()
	if rc == 1 {
		// Publish before cancellation and before returning the worker result.
		s.revoked.Store(true)
		s.cancel()
		e.say("[%s / %s] [FAIL] REVOKED/SUSPENDED: rejecting this source and all its routes; trying other sources.", j.source, j.route)
	}
	if rc != 0 {
		e.say("[%s / %s] [WARN] Candidate verification exit %d; trying next candidate.", j.source, j.route, rc)
		return nil, fmt.Errorf("verify exit %d", rc)
	}
	if ctx.Err() != nil || s.revoked.Load() {
		return nil, fmt.Errorf("source stopped")
	}
	if e.skipRevocation {
		e.say("[%s / %s] [OK] Backend and local certificate checks passed; revocation skipped.", j.source, j.route)
	} else {
		e.say("[%s / %s] [OK] Backend and certificate checks passed.", j.source, j.route)
	}
	return prepared, nil
}
func (e *engine) fetch(ctx context.Context, j routeJob, s *sourceState, work, kind string) []byte {
	dir, err := os.MkdirTemp(work, "source.")
	if err != nil {
		return nil
	}
	defer os.RemoveAll(dir)
	urls := []string{}
	switch j.source {
	case "meow":
		urls = []string{e.meow}
	case "addon":
		urls = []string{e.addon}
	case "spectre":
		raw, err := e.download(ctx, j, e.catalog, dir)
		if err != nil {
			return nil
		}
		path := filepath.Join(dir, "catalog.json")
		if os.WriteFile(path, raw, 0600) != nil {
			return nil
		}
		urls, err = catalog.URLs([]string{path, e.base})
		if err != nil {
			e.say("[%s / %s] [WARN] Catalog rejected: %v", j.source, j.route, err)
			return nil
		}
	default:
		return nil
	}
	for _, url := range urls {
		if ctx.Err() != nil || s.revoked.Load() {
			return nil
		}
		if data, err := e.candidate(ctx, j, s, url, dir, kind); err == nil {
			return data
		}
	}
	return nil
}
func (e *engine) race(ctx context.Context, work, kind string) (routeResult, error) {
	jobs := e.jobs()
	for len(jobs) > 0 {
		if err := ctx.Err(); err != nil {
			return routeResult{}, err
		}
		end := 1
		for end < len(jobs) && jobs[end].source == jobs[0].source {
			end++
		}
		r := e.raceSource(ctx, jobs[:end], work, kind)
		if err := ctx.Err(); err != nil {
			return routeResult{}, err
		}
		if len(r.data) > 0 {
			return r, nil
		}
		jobs = jobs[end:]
	}
	return routeResult{}, fmt.Errorf("all sources failed (network, backend compatibility or certificate checks); old keybox retained")
}

// raceSource joins every route before accepting data or starting the next source.
func (e *engine) raceSource(ctx context.Context, jobs []routeJob, work, kind string) routeResult {
	if len(jobs) == 0 || ctx.Err() != nil {
		return routeResult{}
	}
	sourceCtx, stop := context.WithCancel(ctx)
	s := &sourceState{ctx: sourceCtx, cancel: stop}
	contexts := make([]context.Context, len(jobs))
	cancels := make([]context.CancelFunc, len(jobs))
	for i := range jobs {
		contexts[i], cancels[i] = context.WithCancel(sourceCtx)
	}
	var mu sync.Mutex
	winner := -1
	s.claim = func(j routeJob) bool {
		mu.Lock()
		defer mu.Unlock()
		if sourceCtx.Err() != nil {
			return false
		}
		if winner >= 0 {
			return jobs[winner] == j
		}
		for i := range jobs {
			if jobs[i] == j {
				winner = i
				break
			}
		}
		for i, cancel := range cancels {
			if i != winner {
				cancel()
			}
		}
		return true
	}
	results := make([]routeResult, len(jobs))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j routeJob) {
			defer wg.Done()
			routeCtx := contexts[i]
			select {
			case sem <- struct{}{}:
			case <-routeCtx.Done():
				return
			}
			defer func() { <-sem }()
			if routeCtx.Err() == nil {
				e.say("[%s / %s] [FETCH] Starting", j.source, j.route)
				results[i] = routeResult{job: j, data: e.fetch(routeCtx, j, s, work, kind)}
			}
		}(i, j)
	}
	wg.Wait()
	for _, cancel := range cancels {
		cancel()
	}
	stop()
	if ctx.Err() != nil || s.revoked.Load() {
		return routeResult{}
	}
	if winner < 0 {
		return routeResult{}
	}
	if len(results[winner].data) > 0 {
		return results[winner]
	}
	return routeResult{}
}
