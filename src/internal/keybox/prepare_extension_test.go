package keybox

import (
	"crypto/elliptic"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"testing"
)

func TestPrepareOpaqueAndroidExtensions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		oid   asn1.ObjectIdentifier
		value []byte
	}{
		{"CBOR provisioning info", asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 1, 30}, []byte{0xa1, 0x01, 0x01}},
		{"high tag attestation field", asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 1, 17}, []byte{0x30, 0x07, 0xbf, 0x85, 0x3d, 0x03, 0x02, 0x01, 0x01}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := prepareFixtureCurve(t, "ec", elliptic.P256(), pkix.Extension{Id: tc.oid, Value: tc.value})
			if _, err := x509.ParseCertificate(f.der); err != nil {
				t.Fatal(err)
			}
			xml := []byte("<AndroidAttestation><Keybox>" + prepareTestKey(f, 1, "<NumberOfCertificates>1</NumberOfCertificates>") + "</Keybox></AndroidAttestation>")
			if _, err := Prepare(xml, "tricky-store"); err != nil {
				t.Fatal(err)
			}
			if _, err := Prepare(xml, "oh-my-keymint"); err != nil {
				t.Fatal(err)
			}
		})
	}
	// Malformed outer Extension TLV still must fail.
	if prepareExtensions([]byte{0x30, 0x03, 0x30, 0x05, 0x06}) {
		t.Fatal("accepted truncated extension envelope")
	}
}
