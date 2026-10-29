package keybox

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestIntegrityPrivateCorrespondenceAndAllKeys(t *testing.T) {
	for _, algorithm := range []string{"ec", "rsa"} {
		t.Run(algorithm, func(t *testing.T) {
			first, other := prepareFixtureFor(t, algorithm), prepareFixtureFor(t, algorithm)
			key := prepareTestKey(first, 1, "<NumberOfCertificates>1</NumberOfCertificates>")
			for _, private := range []string{first.private, first.pkcs8} {
				data := prepareTestDocument(strings.Replace(key, first.private, private, 1))
				result := CheckIntegrity(data)
				if result.Status != "valid" || len(result.Keys) != 1 || result.Keys[0].Root != "unknown" {
					t.Fatalf("valid local chain: %+v", result)
				}
				// Fixtures are expired; cryptographic and device-time results must stay distinct.
				if validity, _ := CheckValidity(data, time.Now()); validity.Status != "expired" {
					t.Fatal("fixture no longer checks time separation")
				}
			}
			mismatch := strings.Replace(key, first.private, other.private, 1)
			result := CheckIntegrity(prepareTestDocument(key, mismatch))
			if result.Status != "invalid" || len(result.Keys) != 2 ||
				!result.Keys[0].KeyMatch || !result.Keys[1].PrivateKeyValid || result.Keys[1].KeyMatch ||
				!result.Keys[1].ChainValid {
				t.Fatalf("mismatched second key was accepted: %+v", result)
			}
			for name, invalid := range map[string]string{
				"missing private":              strings.Replace(key, "<PrivateKey format=\"pem\">\n"+first.private+"</PrivateKey>", "", 1),
				"missing chain on another key": prepareTestKey(first, -1, ""),
				"incorrect count":              strings.Replace(key, ">1</NumberOfCertificates>", ">2</NumberOfCertificates>", 1),
			} {
				if result := CheckIntegrity(prepareTestDocument(key, invalid)); result.Status != "invalid" {
					t.Errorf("%s accepted: %+v", name, result)
				}
			}
		})
	}
}

func TestIntegrityRejectsInvalidPrivateArithmeticWithoutPanic(t *testing.T) {
	for _, algorithm := range []string{"ec", "rsa"} {
		t.Run(algorithm, func(t *testing.T) {
			fixture := prepareFixtureFor(t, algorithm)
			block, _ := pem.Decode([]byte(fixture.private))
			var scalar []byte
			if algorithm == "ec" {
				key, err := x509.ParseECPrivateKey(block.Bytes)
				if err != nil {
					t.Fatal(err)
				}
				scalar = key.D.FillBytes(make([]byte, 32))
			} else {
				key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
				if err != nil {
					t.Fatal(err)
				}
				scalar = key.D.Bytes()
			}
			// Keep the DER envelope intact while making the secret exponent invalid.
			bad := bytes.Repeat([]byte{0x7f}, len(scalar))
			if algorithm == "ec" {
				bad = bytes.Repeat([]byte{0xff}, len(scalar))
			}
			block.Bytes = bytes.Replace(block.Bytes, scalar, bad, 1)
			key := prepareTestKey(fixture, 1, "")
			key = strings.Replace(key, fixture.private, string(pem.EncodeToMemory(block)), 1)
			result := CheckIntegrity(prepareTestDocument(key))
			if result.Status != "invalid" || (len(result.Keys) > 0 && result.Keys[0].PrivateKeyValid) {
				t.Fatalf("invalid private arithmetic accepted: %+v", result)
			}
		})
	}
}

func TestIntegrityChainSignaturesAndConstraints(t *testing.T) {
	rootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rootTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic root"},
		NotBefore: time.Unix(0, 0), NotAfter: time.Unix(2000000000, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	create := func(template, parent *x509.Certificate, public, private any) []byte {
		t.Helper()
		der, err := x509.CreateCertificate(rand.Reader, template, parent, public, private)
		if err != nil {
			t.Fatal(err)
		}
		return der
	}
	encode := func(der []byte) string {
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	}
	rootDER := create(rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "synthetic leaf"},
		NotBefore: time.Unix(0, 0), NotAfter: time.Unix(1900000000, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	leafDER := create(leafTemplate, root, &leafKey.PublicKey, rootKey)
	private, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	document := func(leaf, root []byte) []byte {
		f := prepareFixture{algorithm: "ec", private: string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private})), certificate: encode(leaf)}
		key := prepareTestKey(f, 1, "")
		key = strings.Replace(key, "</CertificateChain>", "<Certificate>"+encode(root)+"</Certificate></CertificateChain>", 1)
		return prepareTestDocument(key)
	}
	if result := CheckIntegrity(document(leafDER, rootDER)); result.Status != "valid" {
		t.Fatalf("mixed EC/RSA chain rejected: %+v", result)
	}
	tamperedLeaf := append([]byte(nil), leafDER...)
	tamperedLeaf[len(tamperedLeaf)-1] ^= 1
	tamperedRoot := append([]byte(nil), rootDER...)
	tamperedRoot[len(tamperedRoot)-1] ^= 1
	wrongIssuer := *root
	wrongIssuer.Subject = pkix.Name{CommonName: "different issuer"}
	wrongIssuer.RawSubject = nil
	wrongIssuerLeaf := create(leafTemplate, &wrongIssuer, &leafKey.PublicKey, rootKey)
	notCA := *rootTemplate
	notCA.IsCA = false
	notCADER := create(&notCA, &notCA, &rootKey.PublicKey, rootKey)
	for name, pair := range map[string][2][]byte{
		"tampered child signature": {tamperedLeaf, rootDER},
		"tampered root signature":  {leafDER, tamperedRoot},
		"issuer mismatch":          {wrongIssuerLeaf, rootDER},
		"parent is not CA":         {leafDER, notCADER},
		"reversed chain":           {rootDER, leafDER},
		"incomplete chain":         {leafDER, leafDER},
	} {
		t.Run(name, func(t *testing.T) {
			result := CheckIntegrity(document(pair[0], pair[1]))
			if result.Status != "invalid" || (len(result.Keys) > 0 && result.Keys[0].ChainValid) {
				t.Fatalf("invalid chain accepted: %+v", result)
			}
		})
	}
	for _, data := range [][]byte{nil, []byte("<AndroidAttestation/>"), []byte("<AndroidAttestation><Certificate/></AndroidAttestation>")} {
		if result := CheckIntegrity(data); result.Status != "invalid" {
			t.Fatalf("malformed keybox: %+v", result)
		}
	}
}
