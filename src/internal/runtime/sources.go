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
	wg      sync.WaitGroup
}
type routeResult struct {
	job  routeJob
	data []byte
}

func (e *engine) jobs() []routeJob {
	var jobs []routeJob
	for _, source := range []string{"meow", "spectre", "addon"} {
		enabled := false
		for _, s := range e.sources {
			if source == s {
				enabled = true
			}
		}
		if !enabled {
			continue
		}
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
	e.say("[%s / %s] [OK] Backend and certificate checks passed.", j.source, j.route)
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
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	states := map[string]*sourceState{}
	jobs := e.jobs()
	for _, j := range jobs {
		if states[j.source] == nil {
			c, stop := context.WithCancel(ctx)
			states[j.source] = &sourceState{ctx: c, cancel: stop}
		}
	}
	defer func() {
		for _, s := range states {
			s.cancel()
		}
	}()
	results := make(chan routeResult, len(jobs))
	// A semaphore bounds active routes, including queued manually enabled Addon.
	sem := make(chan struct{}, 6)
	var all sync.WaitGroup
	for _, j := range jobs {
		s := states[j.source]
		s.wg.Add(1)
		all.Add(1)
		go func(j routeJob, s *sourceState) {
			defer all.Done()
			defer s.wg.Done()
			select {
			case sem <- struct{}{}:
			case <-s.ctx.Done():
				results <- routeResult{job: j}
				return
			}
			defer func() { <-sem }()
			var data []byte
			if s.ctx.Err() == nil {
				e.say("[%s / %s] [FETCH] Starting", j.source, j.route)
				data = e.fetch(s.ctx, j, s, work, kind)
			}
			results <- routeResult{job: j, data: data}
		}(j, s)
	}
	// Keep other sources' accepted results while stopping all sibling routes.
	// Wait establishes that every completed Verify exit 1 has been collected.
	var accepted []routeResult
	remaining := len(jobs)
	for remaining > 0 {
		r := <-results
		remaining--
		if len(r.data) > 0 {
			accepted = append(accepted, r)
		}
		for len(accepted) > 0 {
			candidate := accepted[0]
			accepted = accepted[1:]
			s := states[candidate.job.source]
			if s.revoked.Load() {
				continue
			}
			s.cancel()
			s.wg.Wait()
			if s.revoked.Load() {
				continue
			}
			cancel()
			all.Wait()
			if parent.Err() != nil {
				return routeResult{}, parent.Err()
			}
			// Parent cancellation also blocks publication.
			return candidate, nil
		}
	}
	all.Wait()
	return routeResult{}, fmt.Errorf("all sources failed (network, backend compatibility or certificate checks); old keybox retained")
}
