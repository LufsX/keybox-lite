package keybox

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/xml"
	"strconv"
	"strings"
)

// Integrity reports local key correspondence and certificate signatures separately
// from time validity, revocation and root recognition.
type Integrity struct {
	Status string         `json:"status"`
	Keys   []KeyIntegrity `json:"keys"`
}

type KeyIntegrity struct {
	Algorithm       string `json:"algorithm"`
	PrivateKeyValid bool   `json:"private_key_valid"`
	KeyMatch        bool   `json:"key_match"`
	ChainValid      bool   `json:"chain_valid"`
	Root            string `json:"root"`
}

// SHA-256 fingerprints of DER SubjectPublicKeyInfo identify known root keys.
// Google roots: https://android.googleapis.com/attestation/root
// AOSP and Knox roots: https://github.com/purainity/keybox-tools/tree/f6f6e67b165cd99c34d2850056225cd63e5ce0d2/res
// Recognition is not a claim that a device supplies hardware-backed attestation.
var knownRootKeys = map[string]string{
	"feb2ea7551ee316ed4bb443c8293b884dbfdea40b603ee3e4f4a897e4580fbae": "google",
	"3ee44512a1af2beb39c889490c60ea3f82e43f5d5a5532f5ab9419f676cd07ec": "google",
	"d5100c7942ef2e8310dc30ef82729680cf48d690735c3f68179a33c7c370f286": "aosp_ec",
	"f2c4746f545946c100e72297f8f946344d7052f03a2f694221f9c893b0e6f711": "aosp_rsa",
	"6b15a74d196a6453ecc359797e37e9f3385b6190f8ce7b28100aa881b96725d8": "knox",
}

// CheckIntegrity fails closed on every Key, including entries without a chain.
// It does not use the system TLS root store or download certificates.
func CheckIntegrity(data []byte) Integrity {
	result := Integrity{Status: "invalid", Keys: []KeyIntegrity{}}
	cleaned, err := Prepare(data, "tricky_store")
	if err != nil {
		return result
	}
	var doc struct {
		Boxes []struct {
			Keys []struct {
				Algorithm string `xml:"algorithm,attr"`
				Private   string `xml:"PrivateKey"`
				Chain     struct {
					Count        string   `xml:"NumberOfCertificates"`
					Certificates []string `xml:"Certificate"`
				} `xml:"CertificateChain"`
			} `xml:"Key"`
		} `xml:"Keybox"`
	}
	if xml.Unmarshal(cleaned, &doc) != nil {
		return result
	}
	valid := true
	for _, box := range doc.Boxes {
		for _, key := range box.Keys {
			row := KeyIntegrity{Algorithm: strings.ToLower(strings.TrimSpace(key.Algorithm)), Root: "unknown"}
			if row.Algorithm == "ecdsa" {
				row.Algorithm = "ec"
			}
			private := integrityPrivate([]byte(key.Private))
			row.PrivateKeyValid = private != nil
			certs := []*x509.Certificate{}
			for _, text := range key.Chain.Certificates {
				der, ok := preparePEM([]byte(text), "CERTIFICATE")
				if !ok {
					break
				}
				cert, err := x509.ParseCertificate(der)
				if err != nil {
					break
				}
				certs = append(certs, cert)
			}
			if len(certs) > 0 && len(certs) == len(key.Chain.Certificates) {
				if private != nil {
					public, err := x509.MarshalPKIXPublicKey(private.Public())
					leafPublic, leafErr := x509.MarshalPKIXPublicKey(certs[0].PublicKey)
					row.KeyMatch = err == nil && leafErr == nil && bytes.Equal(public, leafPublic)
				}
				row.ChainValid = integrityChain(certs)
				if key.Chain.Count != "" {
					count, err := strconv.Atoi(strings.TrimSpace(key.Chain.Count))
					row.ChainValid = row.ChainValid && err == nil && count == len(certs)
				}
				root := certs[len(certs)-1]
				rootPublic, _ := x509.MarshalPKIXPublicKey(root.PublicKey)
				sum := sha256.Sum256(rootPublic)
				if name := knownRootKeys[hex.EncodeToString(sum[:])]; name != "" {
					row.Root = name
				}
			}
			valid = valid && row.PrivateKeyValid && row.KeyMatch && row.ChainValid
			result.Keys = append(result.Keys, row)
		}
	}
	if valid && len(result.Keys) > 0 {
		result.Status = "valid"
	}
	return result
}

func integrityPrivate(text []byte) crypto.Signer {
	block := singlePEM(text)
	if block == nil {
		return nil
	}
	var key any
	switch block.Type {
	case "PRIVATE KEY":
		key, _ = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		key, _ = x509.ParseECPrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		key, _ = x509.ParsePKCS1PrivateKey(block.Bytes)
	}
	switch k := key.(type) {
	case *rsa.PrivateKey:
		if k == nil {
			return nil
		}
		// Recompute from the secret exponent instead of accepting cached CRT validation.
		k.Precomputed = rsa.PrecomputedValues{}
		if k.Validate() == nil {
			return k
		}
	case *ecdsa.PrivateKey:
		if k != nil && k.D.Sign() > 0 && k.D.Cmp(k.Curve.Params().N) < 0 && k.Curve.IsOnCurve(k.X, k.Y) {
			x, y := k.Curve.ScalarBaseMult(k.D.Bytes())
			if x.Cmp(k.X) == 0 && y.Cmp(k.Y) == 0 {
				return k
			}
		}
	}
	return nil
}

func integrityChain(certs []*x509.Certificate) bool {
	root := certs[len(certs)-1]
	if !bytes.Equal(root.RawIssuer, root.RawSubject) ||
		root.CheckSignature(root.SignatureAlgorithm, root.RawTBSCertificate, root.Signature) != nil {
		return false
	}
	for i := 0; i+1 < len(certs); i++ {
		child, parent := certs[i], certs[i+1]
		if !bytes.Equal(child.RawIssuer, parent.RawSubject) || child.CheckSignatureFrom(parent) != nil {
			return false
		}
	}
	// Validate constraints without mixing the device clock into this result.
	// All certificates must have an overlapping validity interval; actual device
	// time is checked independently by CheckValidity.
	at := certs[0].NotBefore
	until := certs[0].NotAfter
	for _, cert := range certs {
		if cert.NotBefore.After(at) {
			at = cert.NotBefore
		}
		if cert.NotAfter.Before(until) {
			until = cert.NotAfter
		}
	}
	if at.After(until) {
		return false
	}
	roots, intermediates := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(root)
	for i := 1; i+1 < len(certs); i++ {
		intermediates.AddCert(certs[i])
	}
	_, err := certs[0].Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: intermediates, CurrentTime: at,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	return err == nil
}
