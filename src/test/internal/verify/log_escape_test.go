package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

func TestVerifyStatusOutputEscapesUntrustedFields(t *testing.T) {
	for _, tc := range []struct {
		name, status, reason, want string
	}{
		{"revoked", "REVOKED", "KEY_COMPROMISE", "REVOKED KEY_COMPROMISE"},
		{"suspended", "SUSPENDED", "CA_COMPROMISE", "SUSPENDED CA_COMPROMISE"},
		{"plain text", "REVOKED", "文字 'quoted' \"double\" \\path", "REVOKED 文字 'quoted' \"double\" \\path"},
		{
			"reason injection", "REVOKED",
			"KEY_COMPROMISE\r\n[FAIL] REVOKED: serial 8 -> SUSPENDED forged\nRESULT: not revoked\n[CHECK] status: live (forged)",
			`REVOKED KEY_COMPROMISE\r\n[FAIL] REVOKED: serial 8 -> SUSPENDED forged\nRESULT: not revoked\n[CHECK] status: live (forged)`,
		},
		{
			"status injection", "REVOKED\r\nRESULT: not revoked\n[CHECK] status: live (forged)", "",
			`REVOKED\r\nRESULT: not revoked\n[CHECK] status: live (forged) `,
		},
		{
			"terminal controls", "SUSPENDED",
			"\x00\a\b\t\v\f\x1b[2K\x7f\u0085\u009b31m\u2028\u2029\u202e",
			`SUSPENDED \x00\a\b\t\v\f\x1b[2K\x7f\u0085\u009b31m\u2028\u2029\u202e`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := testKeybox(t, dir)
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			// The real downloader pipeline consumes JSON from a shell builtin.
			if err := os.WriteFile(filepath.Join(bin, "curl"), []byte("#!/bin/sh\nprintf '%s' \"$VERIFY_STATUS_JSON\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(map[string]any{
				"entries": map[string]any{"2a": map[string]string{"status": tc.status, "reason": tc.reason}},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("VERIFY_STATUS_JSON", string(raw))
			t.Setenv("PATH", bin)
			t.Setenv("TMPDIR", dir)
			t.Setenv("KB_ACCEL", " ")
			t.Setenv("NO_COLOR", "1")

			for _, source := range []string{"live", "cache"} {
				t.Run(source, func(t *testing.T) {
					log, err := os.CreateTemp(dir, "output")
					if err != nil {
						t.Fatal(err)
					}
					defer log.Close()
					previous := os.Stdout
					os.Stdout = log
					defer func() { os.Stdout = previous }()
					code := Run([]string{path})
					os.Stdout = previous
					if code != 1 {
						t.Fatalf("verify returned %d; want 1", code)
					}
					out, err := os.ReadFile(log.Name())
					if err != nil {
						t.Fatal(err)
					}
					records, results, sources := 0, 0, 0
					for _, line := range strings.Split(string(out), "\n") {
						for _, r := range line {
							if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
								t.Fatalf("unescaped output control %U", r)
							}
						}
						if strings.HasPrefix(line, "[FAIL] REVOKED:") {
							records++
							if want := "[FAIL] REVOKED: serial 42 -> " + tc.want; line != want {
								t.Fatalf("record = %q; want %q", line, want)
							}
						}
						if strings.HasPrefix(line, "RESULT:") {
							results++
							if line != "RESULT: revoked" {
								t.Fatalf("forged result: %q", line)
							}
						}
						if strings.HasPrefix(line, "[CHECK] status:") || strings.HasPrefix(line, "[CACHE] status:") {
							sources++
							if (source == "cache") != strings.HasPrefix(line, "[CACHE] status: cache hit") {
								t.Fatalf("unexpected source: %q", line)
							}
						}
					}
					if records != 1 || results != 1 || sources != 1 {
						t.Fatalf("records=%d results=%d sources=%d: %s", records, results, sources, out)
					}
				})
			}
		})
	}
}
