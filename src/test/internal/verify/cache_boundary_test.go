package verify

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStatusCacheTenMinuteBoundaries(t *testing.T) {
	if statusCacheTTL != 10*time.Minute {
		t.Fatalf("TTL %s", statusCacheTTL)
	}
	dir, file := cacheFixture(t)
	cache, err := openCacheDir(cachePath())
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	now := time.Now()
	for _, tc := range []struct {
		name string
		age  time.Duration
		hit  bool
	}{
		{"new", 0, true},
		{"beyond old TTL", 61 * time.Second, true},
		{"five minutes", 5 * time.Minute, true},
		{"just before expiry", statusCacheTTL - time.Nanosecond, true},
		{"exact expiry", statusCacheTTL, false},
		{"past expiry", statusCacheTTL + time.Nanosecond, false},
		{"future", -time.Nanosecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := cachedStatus{Version: 1, Fetched: now.Add(-tc.age).UnixNano(), URL: live, Raw: json.RawMessage(`{"entries":{}}`)}
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, raw, 0600); err != nil {
				t.Fatal(err)
			}
			_, hit, err := readStatusCache(cache, cacheName(" ")+".json", " ", now)
			if err != nil || hit != tc.hit {
				t.Fatalf("hit %v want %v: %v", hit, tc.hit, err)
			}
		})
	}
	// Cache hits still check the current keybox, and their log exposes the actual TTL.
	path := testKeyboxSerial(t, dir, 41)
	os.Remove(file)
	if code, _ := captureRun(t, []string{path}); code != 0 {
		t.Fatalf("live exit %d", code)
	}
	if code, log := captureRun(t, []string{path}); code != 0 || !strings.Contains(log, "[CACHE] status: cache hit (<10m, ") {
		t.Fatalf("cache exit %d: %s", code, log)
	}
	if requestCount(t) != 1 {
		t.Fatalf("requests %d", requestCount(t))
	}
}
