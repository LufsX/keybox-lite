package keybox

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func datedCertificate(t *testing.T, before, after time.Time) string {
	t.Helper()
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	template := &x509.Certificate{SerialNumber: big.NewInt(42), NotBefore: before, NotAfter: after}
	der, err := x509.CreateCertificate(nil, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestCertificateValidity(t *testing.T) {
	before := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	after := before.Add(time.Hour)
	cert := datedCertificate(t, before, after)
	for _, tc := range []struct {
		name string
		now  time.Time
		want string
	}{
		{"not yet", before.Add(-time.Second), "not_yet_valid"},
		{"start inclusive", before, "valid"},
		{"inside", before.Add(time.Minute), "valid"},
		{"end inclusive", after, "valid"},
		{"expired", after.Add(time.Nanosecond), "expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CheckValidity(document(cert), tc.now)
			if err != nil || got.Status != tc.want || len(got.Certificates) != 1 {
				t.Fatalf("%+v, %v", got, err)
			}
			c := got.Certificates[0]
			if c.Serial != "42" || c.NotBefore != before.Unix() || c.NotAfter != after.Unix() || c.Status != tc.want {
				t.Fatalf("bad certificate details: %+v", c)
			}
		})
	}
	// Inspect the entire chain; an expired issuer must not be hidden by a valid leaf.
	expired := datedCertificate(t, before.Add(-2*time.Hour), before.Add(-time.Hour))
	future := datedCertificate(t, after, after.Add(time.Hour))
	for _, certs := range [][]string{{cert, expired, future}, {future, expired, cert}} {
		got, err := CheckValidity(document(certs...), before)
		if err != nil || got.Status != "expired" || len(got.Certificates) != 3 {
			t.Fatalf("%+v, %v", got, err)
		}
	}
	for _, data := range [][]byte{document(), document(cert, "bad PEM"), document(datedCertificate(t, after, before))} {
		got, err := CheckValidity(data, before)
		if err == nil || got.Status != "unknown" || len(got.Certificates) != 0 {
			t.Fatalf("accepted invalid validity: %+v, %v", got, err)
		}
	}
	// Real certificates spanning the UTCTime pivot and GeneralizedTime.
	for _, year := range []int{1950, 1968, 2049, 2050, 9998} {
		b := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
		got, err := CheckValidity(document(datedCertificate(t, b, b.Add(time.Hour))), b)
		if err != nil || got.Status != "valid" || got.Certificates[0].NotBefore != b.Unix() {
			t.Fatalf("year %d: %+v %v", year, got, err)
		}
	}
}

func TestCertificateTimeRejectsNonRFC5280(t *testing.T) {
	for _, tc := range []struct {
		tag   byte
		value string
	}{
		{0x17, "2501010000Z"}, {0x17, "250101000000+0000"},
		{0x17, "250230000000Z"}, {0x17, "xx0101000000Z"},
		{0x18, "20500101000000.0Z"}, {0x18, "20500101000000+0000"},
		{0x18, "20501301000000Z"}, {0x16, "20500101000000Z"},
	} {
		if _, err := certificateTime(tc.tag, []byte(tc.value)); err == nil {
			t.Fatalf("accepted %q", tc.value)
		}
	}
	// A serial-only DER skeleton must not be treated as time-valid.
	der := []byte{0x30, 0x05, 0x30, 0x03, 0x02, 0x01, 1}
	cert := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	if got, err := CheckValidity(document(cert), time.Now()); err == nil || got.Status != "unknown" {
		t.Fatalf("%+v %v", got, err)
	}
}
