package verify

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The official live endpoint and its accelerated copy are the only status
// sources. The legacy mirror URL is never requested, so the trap fallback
// branch below must never run even while the official response is pending.
func TestRunOfficialStatusOnlyNeverRequestsLegacyMirror(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, liveJSON, fallbackJSON string
		want                         int
	}{
		{"official revocation is applied", `{"entries":{"2a":{"status":"REVOKED"}}}`, `{"entries":{}}`, 1},
		{"clean official list is authoritative", `{"entries":{}}`, `{"entries":{"2a":{"status":"REVOKED"}}}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			keybox := testKeybox(t, dir)
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			stub := `#!/bin/sh
for arg do url=$arg; done
case "$url" in
    *attestation/status)
        printf started > "$FAKE_STARTED"
        while [ ! -f "$FAKE_GATE" ]; do "$FAKE_SLEEP" 0.01; done
        printf '%s' "$FAKE_LIVE"
        ;;
    *)
        printf called > "$FAKE_FALLBACK_CALLED"
        printf '%s' "$FAKE_FALLBACK"
        ;;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(stub), 0700); err != nil {
				t.Fatal(err)
			}
			started := filepath.Join(dir, "started")
			gate := filepath.Join(dir, "gate")
			fallbackCalled := filepath.Join(dir, "fallback")
			t.Setenv("PATH", bin)
			t.Setenv("TMPDIR", dir)
			t.Setenv("KB_ACCEL", "https://proxy.invalid/")
			t.Setenv("FAKE_STARTED", started)
			t.Setenv("FAKE_GATE", gate)
			t.Setenv("FAKE_FALLBACK_CALLED", fallbackCalled)
			t.Setenv("FAKE_SLEEP", sleep)
			t.Setenv("FAKE_LIVE", tc.liveJSON)
			t.Setenv("FAKE_FALLBACK", tc.fallbackJSON)
			done := make(chan int, 1)
			go func() { done <- Run([]string{keybox}) }()
			// Always release and join before testing cleanup, even on a failed assertion.
			defer func() {
				_ = os.WriteFile(gate, nil, 0600)
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("verification failed to return after gate opened")
				}
			}()
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, err := os.Stat(started); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("official downloader never started")
				}
				time.Sleep(10 * time.Millisecond)
			}
			time.Sleep(100 * time.Millisecond)
			if _, err := os.Stat(fallbackCalled); !os.IsNotExist(err) {
				t.Fatalf("fallback ran while official response was pending: %v", err)
			}
			if err := os.WriteFile(gate, nil, 0600); err != nil {
				t.Fatal(err)
			}
			code := <-done
			// Leave the result available for the deferred join.
			done <- code
			if code != tc.want {
				t.Fatalf("Run = %d; want %d", code, tc.want)
			}
			if _, err := os.Stat(fallbackCalled); !os.IsNotExist(err) {
				t.Fatalf("fallback ran despite a valid official response: %v", err)
			}
		})
	}
}

func TestRunFastProxyCancelsDirect(t *testing.T) {
	dir := t.TempDir()
	path := testKeybox(t, dir)
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	// Shell builtins keep the blocked downloader in the inherited process group.
	stub := `#!/bin/sh
for arg do url=$arg; done
if [ "$url" = "https://android.googleapis.com/attestation/status" ]; then
    printf '%s' "$$" > "$FAKE_DIRECT"
    while :; do :; done
fi
while [ ! -f "$FAKE_DIRECT" ]; do :; done
printf '%s' '{"entries":{}}'
`
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("TMPDIR", dir)
	t.Setenv("KB_ACCEL", "https://proxy.invalid/")
	t.Setenv("FAKE_DIRECT", filepath.Join(dir, "direct"))
	start := time.Now()
	if code := Run([]string{path}); code != 0 {
		t.Fatalf("Run = %d; want 0", code)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("fast proxy waited for direct timeout")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "keybox-verify-") {
			t.Fatalf("workdir leaked: %s", file.Name())
		}
	}
}
