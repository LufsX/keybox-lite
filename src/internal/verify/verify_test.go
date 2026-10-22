package verify

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
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
	t.Setenv("KB_STATUS_CACHE", filepath.Join(dir, "status-cache"))
	return testKeyboxSerial(t, dir, 42, dates...)
}

func testKeyboxSerial(t *testing.T, dir string, serial int64, dates ...time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "synthetic"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	if len(dates) == 2 {
		cert.NotBefore, cert.NotAfter = dates[0], dates[1]
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "keybox.xml")
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	data := "<AndroidAttestation><Keybox><Key algorithm=\"ec\"><PrivateKey>" + string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private})) + "</PrivateKey><CertificateChain><Certificate>\n" +
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
			if tc.mode == "fallback" && !strings.Contains(string(requests), live+"\n") {
				t.Fatalf("did not retry direct status after malformed response: %s", requests)
			}
			if tc.mode == "invalid" && len(strings.Fields(string(requests))) != len(statusURLs("https://accel.invalid/", live))+len(statusURLs("https://accel.invalid/", mirror)) {
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

func TestRunRejectsInvalidCryptographyBeforeNetwork(t *testing.T) {
	dir := t.TempDir()
	path := testKeybox(t, dir)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(other)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(original), "<PrivateKey>") + len("<PrivateKey>")
	end := strings.Index(string(original), "</PrivateKey>")
	mismatch := string(original[:start]) + string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})) + string(original[end:])
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "download-attempted")
	stub := "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do if [ \"$1\" = -o ]; then shift; out=$1; fi; shift; done\nprintf '%s' attempted > \"$FAKE_LOG\"\nprintf '%s' '{\"entries\":{}}' > \"$out\"\n"
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("FAKE_LOG", log)
	t.Setenv("TMPDIR", dir)
	if err := os.WriteFile(path, []byte(mismatch), 0600); err != nil {
		t.Fatal(err)
	}
	if got := Run([]string{path}); got != 2 {
		t.Fatalf("mismatched private key passed: %d", got)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("invalid keybox reached downloader: %v", err)
	}
	workdirs, err := filepath.Glob(filepath.Join(dir, "keybox-verify-*"))
	if err != nil || len(workdirs) != 0 {
		t.Fatalf("invalid keybox reached downloader: %v, %v", workdirs, err)
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
