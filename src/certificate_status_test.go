package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"keyboxlite/internal/info"
)

func captureCertificateOutput(t *testing.T, run func() int) (int, string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	previous := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = previous }()
	code := run()
	os.Stdout = previous
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return code, string(raw)
}

func TestCertificateStatusOutputMatchesInfoSerials(t *testing.T) {
	dir := t.TempDir()
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	serials := []string{"100664404977750268806995", "171", "100664404977750268806996"}
	var xml strings.Builder
	xml.WriteString("<AndroidAttestation>")
	for _, sn := range serials {
		n, _ := new(big.Int).SetString(sn, 10)
		cert := &x509.Certificate{
			SerialNumber: n,
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
		}
		der, err := x509.CreateCertificate(nil, cert, cert, key.Public(), key)
		if err != nil {
			t.Fatal(err)
		}
		xml.WriteString("<Certificate>\n")
		xml.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
		xml.WriteString("</Certificate>")
	}
	xml.WriteString("</AndroidAttestation>")
	path := filepath.Join(dir, "keybox.xml")
	list := filepath.Join(dir, "status.json")
	if err := os.WriteFile(path, []byte(xml.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(list, []byte(`{"entries":{"0015510740886364958753":{"status":"REVOKED","reason":"KEY_COMPROMISE"},"00AB":{"status":"SUSPENDED","reason":"CA_COMPROMISE"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	code, out := captureCertificateOutput(t, func() int { return runCheck([]string{path, list}) })
	if code != 1 {
		t.Fatalf("check returned %d: %s", code, out)
	}
	for _, want := range []string{
		"REVOKED: serial " + serials[0] + " -> REVOKED KEY_COMPROMISE",
		"REVOKED: serial " + serials[1] + " -> SUSPENDED CA_COMPROMISE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing certificate status %q: %s", want, out)
		}
	}
	if strings.Contains(out, serials[2]) || strings.Count(out, "REVOKED: serial ") != 2 {
		t.Fatalf("status matched another certificate: %s", out)
	}
	// Inspection uses only the local keybox, even beside a downloaded list.
	t.Setenv("PATH", "")
	code, out = captureCertificateOutput(t, func() int { return info.Run([]string{path}) })
	if code != 0 {
		t.Fatalf("info returned %d: %s", code, out)
	}
	for _, sn := range serials {
		if !strings.Contains(out, `"serial":"`+sn+`"`) {
			t.Errorf("info omitted exact certificate serial %s: %s", sn, out)
		}
	}
	if strings.Contains(out, "KEY_COMPROMISE") || strings.Contains(out, "SUSPENDED") {
		t.Fatalf("info implicitly consumed a nearby status list: %s", out)
	}
}
