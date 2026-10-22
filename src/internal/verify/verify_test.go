package verify

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testKeybox(t *testing.T, dir string, dates ...time.Time) string {
	t.Helper()
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	cert := &x509.Certificate{SerialNumber: big.NewInt(42), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	if len(dates) == 2 {
		cert.NotBefore, cert.NotAfter = dates[0], dates[1]
	}
	der, err := x509.CreateCertificate(nil, cert, cert, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "keybox.xml")
	data := "<AndroidAttestation><Keybox><Key><CertificateChain><Certificate>\n" +
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) +
		"</Certificate></CertificateChain></Key></Keybox></AndroidAttestation>"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunDownloadFallbackAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		wantCode   int
	}{
		{"skip invalid accelerated response", "fallback", 1},
		{"all responses invalid", "invalid", 2},
		{"valid list without matches", "clean", 0},
		{"stale mirror without match is unknown", "mirrorclean", 2},
		{"stale mirror positive match is revoked", "mirrorrevoked", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := testKeybox(t, dir)
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			// Only shell builtins are used; the test performs no networking.
			stub := `#!/bin/sh
out=
url=
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o) out=$2; shift ;;
        https://*) url=$1 ;;
    esac
    shift
done
printf '%s\n' "$url" >> "$FAKE_LOG"
case "$FAKE_MODE:$url" in
    invalid:*|fallback:https://accel.invalid/*|mirrorclean:*attestation/status|mirrorrevoked:*attestation/status) printf '%s' '{"entries":null}' > "$out" ;;
    clean:*|mirrorclean:*) printf '%s' '{"entries":{}}' > "$out" ;;
    *) printf '%s' '{"entries":{"2a":{"status":"REVOKED","reason":"KEY_COMPROMISE"}}}' > "$out" ;;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(stub), 0700); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(dir, "requests.log")
			t.Setenv("PATH", bin)
			t.Setenv("TMPDIR", dir)
			t.Setenv("KB_ACCEL", "https://accel.invalid/")
			t.Setenv("FAKE_MODE", tc.mode)
			t.Setenv("FAKE_LOG", log)
			// A concurrent or older invocation's file must remain untouched.
			legacy := filepath.Join(dir, "keybox-verify.json")
			if err := os.WriteFile(legacy, []byte("do not touch"), 0600); err != nil {
				t.Fatal(err)
			}
			if got := Run([]string{path}); got != tc.wantCode {
				t.Fatalf("Run = %d; want %d", got, tc.wantCode)
			}
			requests, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if tc.mode == "fallback" && string(requests) != "https://accel.invalid/"+live+"\n"+live+"\n" {
				t.Fatalf("did not retry direct status after malformed response: %s", requests)
			}
			if tc.mode == "invalid" && len(strings.Fields(string(requests))) != len(candidates("https://accel.invalid/")) {
				t.Fatalf("did not exhaust candidates: %s", requests)
			}
			if data, err := os.ReadFile(legacy); err != nil || string(data) != "do not touch" {
				t.Fatalf("overwrote shared temporary file: %q, %v", data, err)
			}
			leftovers, err := filepath.Glob(filepath.Join(dir, "keybox-verify-*"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("verification workdirs leaked: %v, %v", leftovers, err)
			}
		})
	}
}

func TestRunWorkdirFailure(t *testing.T) {
	dir := t.TempDir()
	path := testKeybox(t, dir)
	t.Setenv("TMPDIR", filepath.Join(dir, "does-not-exist"))
	if got := Run([]string{path}); got != 2 {
		t.Fatalf("workdir failure returned %d; want 2", got)
	}
}
