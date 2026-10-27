package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func captureRun(t *testing.T, args []string) (int, string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "verify-log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	prev := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = prev }()
	code := Run(args)
	raw, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return code, string(raw)
}

func TestSkipRevocationLocalChecksAndNoNetwork(t *testing.T) {
	for _, kind := range []string{"valid", "structure", "serial", "expired", "future", "private key", "chain signature"} {
		t.Run(kind, func(t *testing.T) {
			dir, _ := cacheFixture(t)
			path := testKeyboxSerial(t, dir, 42)
			want := 0
			switch kind {
			case "structure":
				os.WriteFile(path, []byte("<invalid/>"), 0600)
				want = 2
			case "serial":
				os.WriteFile(path, []byte("<AndroidAttestation><Keybox/></AndroidAttestation>"), 0600)
				want = 2
			case "expired":
				path = testKeyboxSerial(t, dir, 42, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
				want = 3
			case "future":
				path = testKeyboxSerial(t, dir, 42, time.Now().Add(time.Hour), time.Now().Add(2*time.Hour))
				want = 3
			case "private key":
				raw, _ := os.ReadFile(path)
				text := string(raw)
				start := strings.Index(text, "<PrivateKey>")
				end := strings.Index(text, "</PrivateKey>") + len("</PrivateKey>")
				os.WriteFile(path, []byte(text[:start]+"<PrivateKey>invalid</PrivateKey>"+text[end:]), 0600)
				want = 2
			case "chain signature":
				raw, _ := os.ReadFile(path)
				other := testKeyboxSerial(t, t.TempDir(), 43)
				otherRaw, _ := os.ReadFile(other)
				start := strings.Index(string(otherRaw), "<Certificate>")
				end := strings.Index(string(otherRaw), "</Certificate>") + len("</Certificate>")
				text := strings.Replace(string(raw), "</CertificateChain>", string(otherRaw[start:end])+"</CertificateChain>", 1)
				os.WriteFile(path, []byte(text), 0600)
				want = 2
			}
			// Even workdir/cache access is unnecessary when revocation is skipped.
			t.Setenv("TMPDIR", filepath.Join(dir, "missing"))
			code, log := captureRun(t, []string{"--skip-revocation", path})
			if code != want {
				t.Fatalf("exit %d want %d: %s", code, want, log)
			}
			if requestCount(t) != 0 {
				t.Fatal("skip performed network requests")
			}
			if strings.Contains(log, "not revoked") || strings.Contains(log, "none revoked") {
				t.Fatalf("false revocation claim: %s", log)
			}
			skipped := strings.Contains(log, "[SKIP] revocation:") && strings.Contains(log, "RESULT: skipped (revocation)")
			if skipped != (want == 0) {
				t.Fatalf("incorrect skip success: %s", log)
			}
			if kind == "valid" {
				for _, want := range []string{"[OK] validity:", "private key matches; certificate chain signatures valid"} {
					if !strings.Contains(log, want) {
						t.Fatalf("local check missing: %s", log)
					}
				}
			}
		})
	}
}

func TestSkipRevocationIsExplicitAndArgumentsStrict(t *testing.T) {
	dir, _ := cacheFixture(t)
	path := testKeyboxSerial(t, dir, 42) // The fixture's official status revokes serial 42.
	if code, log := captureRun(t, []string{path}); code != 1 || !strings.Contains(log, "RESULT: revoked") {
		t.Fatalf("default verify %d: %s", code, log)
	}
	before := requestCount(t)
	if code, log := captureRun(t, []string{path, "--skip-revocation"}); code != 0 || !strings.Contains(log, "RESULT: skipped (revocation)") {
		t.Fatalf("skip %d: %s", code, log)
	}
	if requestCount(t) != before {
		t.Fatal("skip queried status")
	}
	for _, args := range [][]string{
		{"--unknown", path}, {"--skip-revocation", "--skip-revocation", path}, {path, "another-file"},
	} {
		if code, _ := captureRun(t, args); code != 2 {
			t.Fatalf("%v: %d", args, code)
		}
	}
}
