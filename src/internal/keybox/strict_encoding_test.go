package keybox

import (
	"crypto/elliptic"
	"encoding/asn1"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func TestRejectSkippedPEMBlocks(t *testing.T) {
	fixture := prepareFixtureFor(t, "ec")
	for _, target := range []string{"certificate", "private"} {
		for name, prefix := range map[string]string{
			"invalid base64": "!\n",
			"nested header":  "",
		} {
			t.Run(target+"/"+name, func(t *testing.T) {
				f := fixture
				kind := "CERTIFICATE"
				if target == "private" {
					kind = "EC PRIVATE KEY"
				}
				garbage := "-----BEGIN " + kind + "-----\n" + prefix
				if name == "invalid base64" {
					garbage += "-----END " + kind + "-----\n"
				}
				if target == "certificate" {
					f.certificate = garbage + f.certificate
				} else {
					f.private = garbage + f.private
					if integrityPrivate([]byte(f.private)) != nil {
						t.Fatal("private parser skipped damaged prefix")
					}
				}
				data := prepareTestDocument(prepareTestKey(f, 1, ""))
				if _, err := Prepare(data, "tricky_store"); err == nil {
					t.Fatal("Prepare accepted damaged PEM prefix")
				}
				if CheckIntegrity(data).Status != "invalid" {
					t.Fatal("integrity accepted damaged PEM prefix")
				}
				if target == "certificate" {
					if serials, err := ParseSerials(data); err == nil || len(serials) != 0 {
						t.Fatal("serial parser accepted damaged PEM prefix")
					}
					if validity, err := CheckValidity(data, time.Now()); err == nil || validity.Status != "unknown" {
						t.Fatal("validity parser accepted damaged PEM prefix")
					}
				}
			})
		}
	}
	data := prepareTestDocument(prepareTestKey(fixture, 1, ""))
	data = []byte(strings.Replace(string(data), "-----BEGIN CERTIFICATE-----", "-----BEGIN CERTIFICATE-----\nComment: unsupported\n", 1))
	if _, err := ParseSerials(data); err == nil {
		t.Fatal("serial parser accepted PEM headers")
	}
}

func TestECEncodedPublicPointMatchesScalar(t *testing.T) {
	type ecEncoding struct {
		Version int
		Private []byte
		Curve   asn1.ObjectIdentifier `asn1:"optional,explicit,tag:0"`
		Public  asn1.BitString        `asn1:"optional,explicit,tag:1"`
	}
	type pkcs8Encoding struct {
		Version   int
		Algorithm asn1.RawValue
		Private   []byte
	}
	for _, curve := range []elliptic.Curve{elliptic.P224(), elliptic.P256(), elliptic.P384(), elliptic.P521()} {
		t.Run(curve.Params().Name, func(t *testing.T) {
			fixture := prepareFixtureCurve(t, "ec", curve)
			other := prepareFixtureCurve(t, "ec", curve)
			block, _ := pem.Decode([]byte(other.private))
			var otherEC ecEncoding
			if _, err := asn1.Unmarshal(block.Bytes, &otherEC); err != nil {
				t.Fatal(err)
			}
			for _, encoded := range []string{fixture.private, fixture.pkcs8} {
				block, _ := pem.Decode([]byte(encoded))
				var wrapper pkcs8Encoding
				inner := block.Bytes
				if block.Type == "PRIVATE KEY" {
					if _, err := asn1.Unmarshal(inner, &wrapper); err != nil {
						t.Fatal(err)
					}
					inner = wrapper.Private
				}
				var original ecEncoding
				if _, err := asn1.Unmarshal(inner, &original); err != nil {
					t.Fatal(err)
				}
				for name, mutate := range map[string]func(*ecEncoding){
					"different valid point": func(k *ecEncoding) { k.Public = otherEC.Public },
					"off curve point": func(k *ecEncoding) {
						k.Public.Bytes = make([]byte, len(original.Public.Bytes))
						k.Public.Bytes[0] = 4
					},
					"scalar equals order": func(k *ecEncoding) {
						k.Private = curve.Params().N.FillBytes(make([]byte, len(k.Private)))
					},
					"optional point absent": func(k *ecEncoding) { k.Public = asn1.BitString{} },
				} {
					t.Run(block.Type+"/"+name, func(t *testing.T) {
						key := original
						mutate(&key)
						der, err := asn1.Marshal(key)
						if err != nil {
							t.Fatal(err)
						}
						if block.Type == "PRIVATE KEY" {
							wrapper.Private = der
							der, err = asn1.Marshal(wrapper)
							if err != nil {
								t.Fatal(err)
							}
						}
						f := fixture
						f.private = string(pem.EncodeToMemory(&pem.Block{Type: block.Type, Bytes: der}))
						data := prepareTestDocument(prepareTestKey(f, 1, ""))
						_, err = Prepare(data, "tricky_store")
						wantValid := name == "optional point absent"
						if (err == nil) != wantValid {
							t.Fatalf("Prepare error = %v; want valid %t", err, wantValid)
						}
						if (CheckIntegrity(data).Status == "valid") != wantValid {
							t.Fatalf("unexpected integrity result: %+v", CheckIntegrity(data))
						}
					})
				}
			}
		})
	}
}
