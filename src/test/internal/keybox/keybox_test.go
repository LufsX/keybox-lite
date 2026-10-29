package keybox

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"
)

func certificatePEM(t *testing.T, serial *big.Int) string {
	t.Helper()
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "synthetic test certificate"},
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Unix(2000000000, 0),
	}
	der, err := x509.CreateCertificate(nil, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func document(certificates ...string) []byte {
	var out strings.Builder
	out.WriteString("<?xml version=\"1.0\"?>\n<AndroidAttestation><Keybox><Key><CertificateChain>")
	for _, certificate := range certificates {
		out.WriteString("<Certificate format=\"pem\">\n  ")
		out.WriteString(strings.ReplaceAll(certificate, "\n", "\n  "))
		out.WriteString("\n</Certificate>")
	}
	out.WriteString("</CertificateChain></Key></Keybox></AndroidAttestation>")
	return []byte(out.String())
}

func TestParseSerialsRealCertificates(t *testing.T) {
	// Cover the DER sign padding byte and serials larger than uint64.
	large := new(big.Int).Lsh(big.NewInt(1), 127)
	data := document(certificatePEM(t, big.NewInt(128)), certificatePEM(t, large))
	if !StructureValid(data) {
		t.Fatal("rejected valid indented XML")
	}
	got, err := ParseSerials(data)
	want := []string{"128", large.String()}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseSerials = %v, %v; want %v", got, err, want)
	}
	if !reflect.DeepEqual(Serials(data), want) {
		t.Fatal("Serials disagrees with ParseSerials")
	}
}

func TestStructureValidRejectsMalformedXML(t *testing.T) {
	for _, data := range []string{
		``, `<AndroidAttestation`,
		`<!-- <AndroidAttestation></AndroidAttestation> -->`,
		`<Other><AndroidAttestation></AndroidAttestation></Other>`,
		`<AndroidAttestationFake></AndroidAttestationFake>`,
		`<AndroidAttestation><Key></AndroidAttestation>`,
		`<AndroidAttestation></AndroidAttestation><Other/>`,
		`garbage<AndroidAttestation></AndroidAttestation>`,
		`<AndroidAttestation></AndroidAttestation>garbage`,
	} {
		t.Run(data, func(t *testing.T) {
			if StructureValid([]byte(data)) {
				t.Fatalf("accepted malformed keybox %q", data)
			}
		})
	}
	if !StructureValid([]byte("\n<AndroidAttestation/>\n")) {
		t.Fatal("rejected complete XML root")
	}
}

func TestParseSerialsRejectsPartialResults(t *testing.T) {
	valid := certificatePEM(t, big.NewInt(42))
	badDER := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{0x30, 0x05}}))
	for _, bad := range []string{
		"not PEM", "", badDER,
		strings.ReplaceAll(valid, "CERTIFICATE", "PRIVATE KEY"),
		valid + "unexpected trailing data", valid + valid,
		"unexpected leading data\n" + valid,
		"<Nested/>" + valid,
	} {
		for _, certs := range [][]string{{valid, bad}, {bad, valid}} {
			data := document(certs...)
			if got, err := ParseSerials(data); err == nil || len(got) != 0 {
				t.Fatalf("partial certificates accepted: %v, %v", got, err)
			}
			if got := Serials(data); got == nil || len(got) != 0 {
				t.Fatalf("Serials must return an empty array on error: %v", got)
			}
		}
	}
	// Certificates outside Certificate elements must not be silently included.
	data := []byte("<AndroidAttestation><![CDATA[\n" + valid + "]]></AndroidAttestation>")
	if got, err := ParseSerials(data); err != nil || len(got) != 0 {
		t.Fatalf("PEM outside Certificate elements: %v, %v", got, err)
	}
}

func TestDERRejectsMalformedSerials(t *testing.T) {
	for _, der := range [][]byte{
		nil,
		{0x30, 0x84, 0xff, 0xff, 0xff, 0xff}, // Must not overflow on 32-bit builds.
		{0x30, 0x80, 0, 0},
		{0x30, 0x81, 0x05, 0x30, 0x03, 0x02, 0x01, 0x01},
		{0x30, 0x04, 0x30, 0x02, 0x02, 0x00},                // Empty INTEGER was formerly serial 0.
		{0x30, 0x05, 0x30, 0x03, 0x02, 0x01, 0xff},          // Negative INTEGER.
		{0x30, 0x06, 0x30, 0x04, 0x02, 0x02, 0, 1},          // Nonminimal INTEGER.
		{0x30, 0x07, 0x30, 0x05, 0xa0, 0x00, 0x02, 0x01, 1}, // Empty version.
		{0x30, 0x05, 0x30, 0x03, 0x02, 0x01, 1, 0},          // Trailing DER garbage.
	} {
		if serial, ok := serialOf(der); ok {
			t.Fatalf("accepted malformed DER %x: %x", der, serial)
		}
	}
	for _, offset := range []int{-1, 2, int(^uint(0) >> 1)} {
		if _, _, _, ok := tlv([]byte{2, 0}, offset); ok {
			t.Fatalf("accepted out-of-bounds offset %d", offset)
		}
	}
	// Valid long-form lengths remain supported.
	value := bytes.Repeat([]byte{1}, 128)
	der := append([]byte{4, 0x81, 0x80}, value...)
	if tag, got, next, ok := tlv(der, 0); !ok || tag != 4 || next != len(der) || !bytes.Equal(got, value) {
		t.Fatalf("valid long DER length: %x, %d, %v", got, next, ok)
	}
}

func FuzzParseSerials(f *testing.F) {
	f.Add([]byte("<AndroidAttestation/>"))
	f.Add(document("-----BEGIN CERTIFICATE-----\nMAQwAgIA\n-----END CERTIFICATE-----"))
	f.Fuzz(func(t *testing.T, data []byte) {
		serials, err := ParseSerials(data)
		if err != nil && len(serials) != 0 {
			t.Fatal("returned partial serials on error")
		}
	})
}
