package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"keyboxlite/internal/info"
	"keyboxlite/internal/keybox"
)

func TestReportedRevokedSerialEndToEnd(t *testing.T) {
	dir := t.TempDir()
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	// Use a synthetic certificate with the reported public serial, never the user's private key.
	serial, _ := new(big.Int).SetString("15510740886364958753", 16)
	cert := &x509.Certificate{
		SerialNumber: serial, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(nil, cert, cert, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	data := "<AndroidAttestation><Certificate>\n" +
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) +
		"</Certificate></AndroidAttestation>"
	path, list := filepath.Join(dir, "keybox.xml"), filepath.Join(dir, "status.json")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		data string
		want int
	}{
		{`{"entries":{"15510740886364958753":{"status":"REVOKED","reason":"KEY_COMPROMISE"}}}`, 1},
		{`{"entries":{}}`, 0},
	} {
		if err := os.WriteFile(list, []byte(tc.data), 0600); err != nil {
			t.Fatal(err)
		}
		if got := runCheck([]string{path, list}); got != tc.want {
			t.Fatalf("got %d, want %d", got, tc.want)
		}
	}
}

func TestExpiredCheckAndInfo(t *testing.T) {
	dir := t.TempDir()
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		NotBefore:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	der, err := x509.CreateCertificate(nil, cert, cert, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	data := "<AndroidAttestation><Certificate>\n" + string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) + "</Certificate></AndroidAttestation>"
	path := filepath.Join(dir, "keybox.xml")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	// Expiry is detected even when the caller has no downloaded status list.
	if got := runCheck([]string{path, filepath.Join(dir, "missing.json")}); got != 3 {
		t.Fatalf("got %d, want 3", got)
	}
	out, err := os.CreateTemp(dir, "info")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	old := os.Stdout
	os.Stdout = out
	defer func() { os.Stdout = old }()
	rc := info.Run([]string{path})
	os.Stdout = old
	if rc != 0 {
		t.Fatalf("info returned %d", rc)
	}
	raw, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Exists         bool            `json:"exists"`
		StructureValid bool            `json:"structure_valid"`
		Serials        []string        `json:"serials"`
		Validity       keybox.Validity `json:"validity"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Exists || !result.StructureValid || len(result.Serials) != 1 || result.Validity.Status != "expired" || len(result.Validity.Certificates) != 1 {
		t.Fatalf("bad info contract: %s", raw)
	}
	if got := result.Validity.Certificates[0].NotAfter; got != cert.NotAfter.Unix() {
		t.Fatalf("expiry %d", got)
	}
}
