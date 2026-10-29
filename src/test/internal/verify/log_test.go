package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Covers the two failure exit codes after a valid keybox: an official status
// list that cannot be fetched (2) and an official revocation match (1). The
// legacy mirror URL is never requested, so stale-mirror wording must be absent.
func TestOfficialStatusUnavailableAndRevokedLogSeverityAndPlainOutput(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       string
		wantCode   int
		wantResult string
		wantLine   string
	}{
		{"official status unavailable", "failed", 2, "RESULT: unknown", "[FAIL] current Google status unavailable"},
		{"official status revoked", "live", 1, "RESULT: revoked", "[FAIL] REVOKED: serial 42"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := cacheFixture(t)
			path := testKeyboxSerial(t, dir, 42)
			t.Setenv("VERIFY_CACHE_MODE", tc.mode)
			t.Setenv("TERM", "xterm-256color")
			t.Setenv("NO_COLOR", "")
			log, err := os.Create(filepath.Join(dir, "human.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			previous := os.Stdout
			os.Stdout = log
			defer func() { os.Stdout = previous }()
			code := Run([]string{path})
			os.Stdout = previous
			if code != tc.wantCode {
				t.Fatalf("Run = %d; want %d", code, tc.wantCode)
			}
			raw, err := os.ReadFile(log.Name())
			if err != nil {
				t.Fatal(err)
			}
			out := string(raw)
			for _, want := range []string{
				tc.wantResult,
				tc.wantLine,
				"[FAIL]",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q in %s", want, out)
				}
			}
			if strings.Contains(out, "stale mirror") || strings.Contains(out, "mirror") {
				t.Errorf("legacy mirror wording in output: %s", out)
			}
			if strings.Contains(out, "\x1b") {
				t.Errorf("ANSI escapes in redirected output: %q", out)
			}
		})
	}
}
