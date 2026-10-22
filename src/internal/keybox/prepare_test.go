package keybox

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

type prepareFixture struct {
	algorithm, private, pkcs8, certificate string
	der                                    []byte
}

func prepareFixtureFor(t testing.TB, algorithm string) prepareFixture {
	t.Helper()
	return prepareFixtureCurve(t, algorithm, elliptic.P256())
}

func prepareFixtureCurve(t testing.TB, algorithm string, curve elliptic.Curve, extensions ...pkix.Extension) prepareFixture {
	t.Helper()
	var private any
	var public any
	var der []byte
	var kind string
	if algorithm == "ec" {
		key, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		private, public = key, &key.PublicKey
		der, err = x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		kind = "EC PRIVATE KEY"
	} else {
		key, err := rsa.GenerateKey(rand.Reader, 1024)
		if err != nil {
			t.Fatal(err)
		}
		private, public = key, &key.PublicKey
		der = x509.MarshalPKCS1PrivateKey(key)
		kind = "RSA PRIVATE KEY"
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "generated Prepare test"},
		// Expired deliberately: Prepare must not evaluate time validity.
		NotBefore: time.Unix(0, 0), NotAfter: time.Unix(86400, 0),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:        x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtraExtensions: extensions,
	}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	return prepareFixture{
		algorithm:   algorithm,
		private:     string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})),
		pkcs8:       string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})),
		certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate})),
		der:         certificate,
	}
}

func prepareTestKey(f prepareFixture, count int, declaration string) string {
	s := "<Key algorithm=\"" + f.algorithm + "\"><PrivateKey format=\"pem\">\n" + f.private + "</PrivateKey>"
	if count >= 0 {
		s += "<CertificateChain>" + declaration
		for i := 0; i < count; i++ {
			s += "<Certificate format=\"pem\">\n" + f.certificate + "</Certificate>"
		}
		s += "</CertificateChain>"
	}
	return s + "</Key>"
}

func prepareTestDocument(keys ...string) []byte {
	return []byte("<?xml version=\"1.0\"?>\n<AndroidAttestation><NumberOfKeyboxes>1</NumberOfKeyboxes><Keybox DeviceID=\"generated\">" + strings.Join(keys, "") + "</Keybox></AndroidAttestation>")
}

func TestPrepareBackendsAndKeyEncodings(t *testing.T) {
	ec, rsa := prepareFixtureFor(t, "ec"), prepareFixtureFor(t, "rsa")
	for _, f := range []prepareFixture{ec, rsa} {
		for _, private := range []string{f.private, f.pkcs8} {
			f.private = private
			for _, backend := range []string{"tricky_store", "tricky-store", "tricky_store_legacy", "tricky-store-legacy", "tee_simulator", "tee-simulator", "oh_my_keymint", "oh-my-keymint"} {
				t.Run(f.algorithm+"/"+backend, func(t *testing.T) {
					input := prepareTestDocument(prepareTestKey(f, 2, "<NumberOfCertificates>2</NumberOfCertificates>"))
					got, err := Prepare(input, backend)
					if backend == "oh_my_keymint" || backend == "oh-my-keymint" {
						if f.algorithm == "rsa" {
							if err == nil || got != nil {
								t.Fatal("OhMyKeymint accepted RSA-only keybox")
							}
							return
						}
					}
					if err != nil || !StructureValid(got) || !bytes.HasSuffix(got, []byte("\n")) {
						t.Fatalf("Prepare result: bytes=%d, error=%v", len(got), err)
					}
					serials, err := ParseSerials(got)
					if err != nil || len(serials) != 2 {
						t.Fatalf("prepared XML not compatible with ParseSerials: %v", err)
					}
				})
			}
		}
	}
	// A certified EC key plus an uncertified RSA key is accepted only by TS.
	mixed := prepareTestDocument(prepareTestKey(ec, 1, ""), prepareTestKey(rsa, -1, ""))
	for _, backend := range []string{"tricky_store", "tricky_store_legacy", "tee_simulator", "oh_my_keymint"} {
		_, err := Prepare(mixed, backend)
		wantOK := strings.HasPrefix(backend, "tricky")
		if (err == nil) != wantOK {
			t.Fatalf("uncertified RSA on %s: %v", backend, err)
		}
	}
}

func TestPrepareBackendCertificateCounts(t *testing.T) {
	ec := prepareFixtureFor(t, "ec")
	for _, tc := range []struct {
		name, declaration string
		count             int
		ts, tee, omk      bool
	}{
		{"missing chain", "", -1, false, false, false},
		{"empty chain", "", 0, false, false, false},
		{"one certificate", "<NumberOfCertificates>1</NumberOfCertificates>", 1, true, false, true},
		{"two certificates", "<NumberOfCertificates>2</NumberOfCertificates>", 2, true, true, true},
		{"missing declaration", "", 2, true, true, false},
		{"wrong declaration", "<NumberOfCertificates>1</NumberOfCertificates>", 2, true, true, false},
		{"invalid declaration", "<NumberOfCertificates>secret</NumberOfCertificates>", 2, true, true, false},
		{"empty declaration", "<NumberOfCertificates/>", 2, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := prepareTestDocument(prepareTestKey(ec, tc.count, tc.declaration))
			for backend, want := range map[string]bool{"tricky_store": tc.ts, "tee_simulator": tc.tee, "oh_my_keymint": tc.omk} {
				got, err := Prepare(data, backend)
				if (err == nil) != want || (err != nil && got != nil) {
					t.Fatalf("%s: expected accepted=%v, got %v", backend, want, err)
				}
			}
		})
	}
}

func TestPrepareCleansAndIsIdempotent(t *testing.T) {
	ec := prepareFixtureFor(t, "ec")
	data := string(prepareTestDocument(prepareTestKey(ec, 1, "<NumberOfCertificates>1</NumberOfCertificates>")))
	data = "\ufeff<!-- private watermark --><!---->" + data
	data = strings.ReplaceAll(data, "<Keybox", "<Key\u200bbox")
	data = strings.ReplaceAll(data, "generated", "gen\u200cer\u200dated\u2060\ufeff")
	data = strings.ReplaceAll(data, "-----BEGIN", "-----BE\u200bGIN")
	data = strings.ReplaceAll(data, "algorithm=\"ec\"", "algorithm=\"ec&#x200b;\"")
	data = strings.ReplaceAll(data, "<PrivateKey", "<!-- another watermark --><PrivateKey")
	got, err := Prepare([]byte(data), "oh_my_keymint")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"watermark", "<!--", "\ufeff", "\u200b", "\u200c", "\u200d", "\u2060"} {
		if bytes.Contains(got, []byte(text)) {
			t.Fatalf("cleaned XML contains removed marker %q", text)
		}
	}
	again, err := Prepare(got, "oh_my_keymint")
	if err != nil || !bytes.Equal(again, got) {
		t.Fatalf("Prepare is not idempotent: %v", err)
	}
}

func TestPrepareRejectsStructureAndDoesNotLeakInput(t *testing.T) {
	ec := prepareFixtureFor(t, "ec")
	key := prepareTestKey(ec, 1, "<NumberOfCertificates>1</NumberOfCertificates>")
	valid := string(prepareTestDocument(key))
	for name, data := range map[string]string{
		"empty":                      "",
		"root only":                  "<AndroidAttestation/>",
		"comment markers":            "<AndroidAttestation><!-- <Keybox><Key/></Keybox> --></AndroidAttestation>",
		"text markers":               "<AndroidAttestation><![CDATA[<Keybox><Key/></Keybox>]]></AndroidAttestation>",
		"wrapped Keybox":             strings.Replace(valid, key, "<Wrapper>"+key+"</Wrapper>", 1),
		"misplaced key":              "<AndroidAttestation>" + key + "</AndroidAttestation>",
		"misplaced certificate":      strings.Replace(valid, "</Keybox>", "<Certificate>SECRET_DO_NOT_ECHO</Certificate></Keybox>", 1),
		"nested private":             strings.Replace(valid, ec.private, "<Nested>"+ec.private+"</Nested>", 1),
		"missing private":            strings.Replace(valid, "<PrivateKey format=\"pem\">\n"+ec.private+"</PrivateKey>", "", 1),
		"duplicate private":          strings.Replace(valid, "</PrivateKey>", "</PrivateKey><PrivateKey>"+ec.private+"</PrivateKey>", 1),
		"duplicate chain":            strings.Replace(valid, "</CertificateChain>", "</CertificateChain><CertificateChain/>", 1),
		"duplicate declaration":      strings.Replace(valid, "</NumberOfCertificates>", "</NumberOfCertificates><NumberOfCertificates>1</NumberOfCertificates>", 1),
		"duplicate algorithm":        strings.Replace(valid, "algorithm=\"ec\"", "algorithm=\"ec\" algorithm=\"ec\"", 1),
		"unknown algorithm":          strings.Replace(valid, "algorithm=\"ec\"", "algorithm=\"SECRET_DO_NOT_ECHO\"", 1),
		"invalid private format":     strings.Replace(valid, "<PrivateKey format=\"pem\">", "<PrivateKey format=\"der\">", 1),
		"invalid certificate format": strings.Replace(valid, "<Certificate format=\"pem\">", "<Certificate format=\"der\">", 1),
		"mismatched boxes":           strings.Replace(valid, "<NumberOfKeyboxes>1", "<NumberOfKeyboxes>2", 1),
		"empty box":                  strings.Replace(valid, key, "", 1),
		"multiple roots":             valid + "<SECRET_DO_NOT_ECHO/>",
		"trailing text":              valid + "SECRET_DO_NOT_ECHO",
		"namespace":                  strings.Replace(valid, "<AndroidAttestation>", "<AndroidAttestation xmlns=\"SECRET_DO_NOT_ECHO\">", 1),
		"directive":                  "<!DOCTYPE AndroidAttestation [<!ENTITY secret 'SECRET_DO_NOT_ECHO'>]>" + valid,
		"instruction":                strings.Replace(valid, key, "<?SECRET_DO_NOT_ECHO test?>"+key, 1),
		"invalid XML":                strings.Replace(valid, "</PrivateKey>", "</SECRET_DO_NOT_ECHO>", 1),
		"invalid UTF8":               valid + string([]byte{0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Prepare([]byte(data), "tricky_store")
			if err == nil || got != nil {
				t.Fatalf("accepted invalid structure (%d output bytes)", len(got))
			}
			if strings.Contains(err.Error(), "SECRET_DO_NOT_ECHO") || strings.Contains(err.Error(), ec.private) || strings.Contains(err.Error(), "BEGIN") {
				t.Fatal("error leaked input")
			}
		})
	}
	if got, err := Prepare([]byte(valid), "SECRET_DO_NOT_ECHO"); err == nil || got != nil || strings.Contains(err.Error(), "SECRET_DO_NOT_ECHO") {
		t.Fatal("unsupported backend accepted or leaked")
	}
	if got, err := Prepare(bytes.Repeat([]byte("x"), maxPrepareBytes+1), "tricky_store"); err == nil || got != nil {
		t.Fatal("oversized input accepted")
	}
}

func TestPrepareRejectsMalformedPEMAndDER(t *testing.T) {
	ec, rsa := prepareFixtureFor(t, "ec"), prepareFixtureFor(t, "rsa")
	valid := string(prepareTestDocument(prepareTestKey(ec, 1, "<NumberOfCertificates>1</NumberOfCertificates>")))
	for name, private := range map[string]string{
		"empty":               "",
		"fictional key":       "-----BEGIN EC PRIVATE KEY-----\nMAMCAQE=\n-----END EC PRIVATE KEY-----",
		"wrong algorithm":     rsa.private,
		"truncated":           ec.private[:len(ec.private)/2],
		"leading text":        "SECRET_DO_NOT_ECHO\n" + ec.private,
		"trailing text":       ec.private + "SECRET_DO_NOT_ECHO",
		"multiple PEM blocks": ec.private + ec.private,
		"encrypted":           strings.ReplaceAll(ec.private, "EC PRIVATE KEY", "ENCRYPTED PRIVATE KEY"),
	} {
		t.Run("private/"+name, func(t *testing.T) {
			got, err := Prepare([]byte(strings.Replace(valid, ec.private, private, 1)), "tricky_store")
			if err == nil || got != nil || strings.Contains(err.Error(), "SECRET_DO_NOT_ECHO") {
				t.Fatal("invalid private key accepted or leaked")
			}
		})
	}
	certBlock := func(der []byte) string {
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	}
	for name, certificate := range map[string]string{
		"empty":               "",
		"serial only DER":     certBlock([]byte{0x30, 5, 0x30, 3, 2, 1, 1}),
		"truncated DER":       certBlock(ec.der[:len(ec.der)-1]),
		"trailing DER":        certBlock(append(append([]byte{}, ec.der...), 0)),
		"wrong algorithm":     rsa.certificate,
		"leading text":        "SECRET_DO_NOT_ECHO\n" + ec.certificate,
		"trailing text":       ec.certificate + "SECRET_DO_NOT_ECHO",
		"multiple PEM blocks": ec.certificate + ec.certificate,
	} {
		t.Run("certificate/"+name, func(t *testing.T) {
			got, err := Prepare([]byte(strings.Replace(valid, ec.certificate, certificate, 1)), "tricky_store")
			if err == nil || got != nil || strings.Contains(err.Error(), "SECRET_DO_NOT_ECHO") {
				t.Fatal("invalid certificate accepted or leaked")
			}
		})
	}
	// Chain issuers may use a different algorithm from the leaf.
	mixedChain := prepareTestDocument(prepareTestKey(ec, 2, "<NumberOfCertificates>2</NumberOfCertificates>"))
	first := bytes.Index(mixedChain, []byte(ec.certificate))
	second := bytes.Index(mixedChain[first+len(ec.certificate):], []byte(ec.certificate)) + first + len(ec.certificate)
	mixedChain = append(append(append([]byte{}, mixedChain[:second]...), []byte(rsa.certificate)...), mixedChain[second+len(ec.certificate):]...)
	if _, err := Prepare(mixedChain, "tee_simulator"); err != nil {
		t.Fatalf("mixed-algorithm issuer chain rejected: %v", err)
	}
}

func TestPreparePrivateDERGuards(t *testing.T) {
	// Entirely fictional DER integers: structure may be readable without
	// claiming that RSA factors or EC scalar/public coordinates match.
	fakeRSA := struct {
		Version                     int
		N, E, D, P, Q, DP, DQ, QInv *big.Int
	}{0, big.NewInt(15), big.NewInt(3), big.NewInt(3), big.NewInt(3), big.NewInt(5), big.NewInt(1), big.NewInt(3), big.NewInt(2)}
	der, err := asn1.Marshal(fakeRSA)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := prepareSequence(der)
	if !prepareRSA(body, true) {
		t.Fatal("well-shaped fictional key should be structurally readable")
	}
	for _, broken := range [][]byte{nil, body[:len(body)-1], append(append([]byte{}, body...), 0)} {
		if prepareRSA(broken, true) {
			t.Fatal("malformed RSA structure accepted")
		}
	}
}

func TestPrepareECCurvesAndMalformedParameters(t *testing.T) {
	for _, curve := range []elliptic.Curve{elliptic.P224(), elliptic.P256(), elliptic.P384(), elliptic.P521()} {
		t.Run(curve.Params().Name, func(t *testing.T) {
			fixture := prepareFixtureCurve(t, "ec", curve)
			for _, private := range []string{fixture.private, fixture.pkcs8} {
				fixture.private = private
				data := prepareTestDocument(prepareTestKey(fixture, 1, "<NumberOfCertificates>1</NumberOfCertificates>"))
				if _, err := Prepare(data, "oh-my-keymint"); err != nil {
					t.Fatalf("supported EC encoding rejected: %v", err)
				}
			}
		})
	}
	fixture := prepareFixtureFor(t, "ec")
	block, _ := pem.Decode([]byte(fixture.private))
	type ecEncoding struct {
		Version int
		Private []byte
		Curve   asn1.ObjectIdentifier `asn1:"optional,explicit,tag:0"`
		Public  asn1.BitString        `asn1:"optional,explicit,tag:1"`
	}
	var original ecEncoding
	if _, err := asn1.Unmarshal(block.Bytes, &original); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ecEncoding){
		"zero scalar":     func(k *ecEncoding) { k.Private = make([]byte, 32) },
		"short scalar":    func(k *ecEncoding) { k.Private = []byte{1} },
		"wrong version":   func(k *ecEncoding) { k.Version = 2 },
		"missing curve":   func(k *ecEncoding) { k.Curve = nil },
		"unknown curve":   func(k *ecEncoding) { k.Curve = asn1.ObjectIdentifier{1, 2, 3} },
		"truncated point": func(k *ecEncoding) { k.Public = asn1.BitString{Bytes: []byte{4}, BitLength: 8} },
	} {
		t.Run(name, func(t *testing.T) {
			key := original
			mutate(&key)
			der, err := asn1.Marshal(key)
			if err != nil {
				t.Fatal(err)
			}
			private := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
			if preparePrivate(private, "ec") {
				t.Fatal("malformed EC key accepted")
			}
		})
	}
}

func FuzzPrepare(f *testing.F) {
	ec := prepareFixtureFor(f, "ec")
	f.Add(prepareTestDocument(prepareTestKey(ec, 2, "<NumberOfCertificates>2</NumberOfCertificates>")), "oh-my-keymint")
	f.Add([]byte("<AndroidAttestation/>"), "tricky_store")
	f.Add([]byte("\ufeff<!--test--><AndroidAttestation><Keybox/></AndroidAttestation>"), "oh_my_keymint")
	f.Fuzz(func(t *testing.T, data []byte, backend string) {
		got, err := Prepare(data, backend)
		if err != nil && got != nil {
			t.Fatal("partial output on error")
		}
		if err == nil {
			again, err := Prepare(got, backend)
			if err != nil || !bytes.Equal(again, got) {
				t.Fatal("successful Prepare must be idempotent")
			}
		}
	})
}
