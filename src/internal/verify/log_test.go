package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMirrorLogSeverityAndPlainOutput(t *testing.T) {
	for _, tc := range []struct {
		mode       string
		wantCode   int
		wantResult string
		wantFail   bool
	}{
		{"mirrorclean", 2, "unknown", false},
		{"mirrorrevoked", 1, "revoked", true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
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
				"[WARN] stale mirror: newer revocations may be missing",
				"RESULT: " + tc.wantResult,
			} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q in %s", want, out)
				}
			}
			if strings.Contains(out, "[FAIL]") != tc.wantFail {
				t.Errorf("incorrect failure severity: %s", out)
			}
			if tc.wantCode == 2 && !strings.Contains(out, "[WARN] current revocation status unavailable") {
				t.Errorf("unknown status warning missing: %s", out)
			}
			if strings.Contains(out, "\x1b") {
				t.Errorf("ANSI escapes in redirected output: %q", out)
			}
		})
	}
}
