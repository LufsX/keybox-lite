package keybox

import (
	"bytes"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxPrepareBytes = 1 << 20

// Prepare cleans a keybox and checks its structure and backend compatibility.
// DER checks validate encoding, field structure and key/leaf-certificate algorithms.
// EC scalar/public-point consistency is checked. RSA arithmetic, key/certificate
// correspondence, signatures and trust are not evaluated here.
// Time validity and revocation must be checked separately before installation.
func Prepare(data []byte, backend string) ([]byte, error) {
	backend = strings.ReplaceAll(backend, "-", "_")
	switch backend {
	case "tricky_store", "tricky_store_legacy", "tee_simulator", "tee_simulator_rs", "oh_my_keymint":
	default:
		return nil, errors.New("unsupported keybox backend")
	}
	if len(data) == 0 || len(data) > maxPrepareBytes || !utf8.Valid(data) {
		return nil, errors.New("keybox must be UTF-8 and between 1 byte and 1 MiB")
	}
	cleaned := prepareStripHidden(string(data))
	decoder := xml.NewDecoder(strings.NewReader(strings.TrimSpace(cleaned)))
	var output bytes.Buffer
	encoder := xml.NewEncoder(&output)
	type frame struct {
		name string
		text strings.Builder
	}
	stack := []*frame{}
	var key *prepareKey
	roots, boxes, boxKeys, certified := 0, 0, 0, 0
	hasEC, hasRSA, rootClosed, declaration := false, false, false, false
	boxCount, boxCountSeen := -1, false
	fail := func(reason string) ([]byte, error) { return nil, errors.New(reason) }
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail("invalid keybox XML")
		}
		switch t := token.(type) {
		case xml.Comment:
			continue
		case xml.Directive:
			return fail("XML directives are not supported")
		case xml.ProcInst:
			if t.Target != "xml" || declaration || roots != 0 {
				return fail("unexpected XML processing instruction")
			}
			declaration = true
		case xml.StartElement:
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1].name
			}
			if t.Name.Space != "" || !prepareChild(parent, t.Name.Local) {
				return fail("invalid keybox element hierarchy")
			}
			attrs := map[string]string{}
			for i, attr := range t.Attr {
				attr.Value = prepareStripHidden(attr.Value)
				t.Attr[i] = attr
				if attr.Name.Space != "" {
					return fail("XML namespaces are not supported")
				}
				if _, exists := attrs[attr.Name.Local]; exists {
					return fail("duplicate XML attribute")
				}
				attrs[attr.Name.Local] = attr.Value
			}
			token = t
			switch t.Name.Local {
			case "AndroidAttestation":
				roots++
				if roots != 1 || rootClosed {
					return fail("expected one AndroidAttestation root")
				}
			case "Keybox":
				boxes++
				boxKeys = 0
			case "NumberOfKeyboxes":
				if boxCountSeen {
					return fail("duplicate NumberOfKeyboxes")
				}
				boxCountSeen = true
			case "Key":
				algorithm := strings.ToLower(strings.TrimSpace(attrs["algorithm"]))
				if algorithm == "ecdsa" {
					algorithm = "ec"
				}
				if algorithm != "rsa" && algorithm != "ec" {
					return fail("Key algorithm must be ec, ecdsa or rsa")
				}
				key = &prepareKey{algorithm: algorithm}
				boxKeys++
				hasEC = hasEC || algorithm == "ec"
				hasRSA = hasRSA || algorithm == "rsa"
			case "PrivateKey":
				if key.private {
					return fail("duplicate PrivateKey")
				}
				key.private = true
				if format, exists := attrs["format"]; exists && strings.ToLower(strings.TrimSpace(format)) != "pem" {
					return fail("PrivateKey format must be pem")
				}
			case "CertificateChain":
				if key.chain {
					return fail("duplicate CertificateChain")
				}
				key.chain = true
			case "NumberOfCertificates":
				if key.countSeen {
					return fail("duplicate NumberOfCertificates")
				}
				key.countSeen = true
			case "Certificate":
				if format, exists := attrs["format"]; exists && strings.ToLower(strings.TrimSpace(format)) != "pem" {
					return fail("Certificate format must be pem")
				}
			}
			stack = append(stack, &frame{name: t.Name.Local})
		case xml.CharData:
			t = xml.CharData(prepareStripHidden(string(t)))
			token = t
			if len(stack) == 0 {
				if len(bytes.TrimSpace(t)) != 0 {
					return fail("text outside keybox root")
				}
			} else {
				f := stack[len(stack)-1]
				switch f.name {
				case "PrivateKey", "Certificate", "NumberOfCertificates", "NumberOfKeyboxes":
					f.text.Write(t)
				default:
					if len(bytes.TrimSpace(t)) != 0 {
						return fail("unexpected text in keybox structure")
					}
				}
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return fail("invalid keybox XML")
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			switch f.name {
			case "PrivateKey":
				if !preparePrivate([]byte(f.text.String()), key.algorithm) {
					return fail("invalid or incompatible private key PEM/DER")
				}
			case "Certificate":
				der, ok := preparePEM([]byte(f.text.String()), "CERTIFICATE")
				algorithm, valid := prepareCertificate(der)
				if !ok || !valid {
					return fail("invalid certificate PEM/DER")
				}
				if key.certificates == 0 && algorithm != key.algorithm {
					return fail("leaf certificate algorithm differs from Key algorithm")
				}
				key.certificates++
			case "NumberOfCertificates":
				value := strings.TrimSpace(f.text.String())
				key.count, key.countValid = prepareCount(value)
			case "NumberOfKeyboxes":
				var valid bool
				boxCount, valid = prepareCount(strings.TrimSpace(f.text.String()))
				if !valid {
					return fail("invalid NumberOfKeyboxes")
				}
			case "Key":
				if !key.private {
					return fail("Key requires a PrivateKey")
				}
				if key.certificates > 0 {
					certified++
				}
				if backend == "tee_simulator" && key.certificates < 2 {
					return fail("TEESimulator requires at least two certificates per Key")
				}
				if backend == "oh_my_keymint" && (key.certificates == 0 || !key.countSeen || !key.countValid || key.count != key.certificates) {
					return fail("OhMyKeymint requires a nonempty chain with an accurate NumberOfCertificates per Key")
				}
				key = nil
			case "Keybox":
				if boxKeys == 0 {
					return fail("Keybox requires at least one Key")
				}
			case "AndroidAttestation":
				rootClosed = true
			}
		}
		if err := encoder.EncodeToken(token); err != nil {
			return fail("cannot encode keybox XML")
		}
	}
	if roots != 1 || !rootClosed || len(stack) != 0 || boxes == 0 || certified == 0 {
		return fail("keybox requires Keybox entries and at least one certified Key")
	}
	if boxCountSeen && boxCount != boxes {
		return fail("NumberOfKeyboxes does not match Keybox entries")
	}
	if backend == "oh_my_keymint" && !hasEC {
		return fail("OhMyKeymint requires an EC Key")
	}
	if backend == "tee_simulator" && (!hasEC || !hasRSA) {
		return fail("TEESimulator requires both an EC and an RSA Key")
	}
	if err := encoder.Flush(); err != nil {
		return fail("cannot encode keybox XML")
	}
	out := bytes.TrimSpace(output.Bytes())
	if len(out)+1 > maxPrepareBytes {
		return fail("cleaned keybox exceeds 1 MiB")
	}
	return append(out, '\n'), nil
}

func prepareStripHidden(text string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\ufeff', '\u200b', '\u200c', '\u200d', '\u2060':
			return -1
		}
		return r
	}, text)
}

type prepareKey struct {
	algorithm                 string
	private, chain, countSeen bool
	countValid                bool
	count, certificates       int
}

func prepareChild(parent, child string) bool {
	switch parent {
	case "":
		return child == "AndroidAttestation"
	case "AndroidAttestation":
		return child == "NumberOfKeyboxes" || child == "Keybox"
	case "Keybox":
		return child == "Key"
	case "Key":
		return child == "PrivateKey" || child == "CertificateChain"
	case "CertificateChain":
		return child == "NumberOfCertificates" || child == "Certificate"
	}
	return false
}

func prepareCount(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(value)
	return n, err == nil && n <= maxPrepareBytes
}

// singlePEM rejects prefixes that pem.Decode would silently skip while searching
// for a later valid block. Indentation and surrounding whitespace remain supported.
func singlePEM(text []byte) *pem.Block {
	text = bytes.TrimSpace(normalize(text))
	if !bytes.HasPrefix(text, []byte("-----BEGIN ")) ||
		bytes.Count(text, []byte("-----BEGIN ")) != 1 ||
		bytes.Count(text, []byte("-----END ")) != 1 {
		return nil
	}
	block, rest := pem.Decode(text)
	if block == nil || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil
	}
	return block
}

func preparePEM(text []byte, kind string) ([]byte, bool) {
	block := singlePEM(text)
	if block == nil || block.Type != kind {
		return nil, false
	}
	return block.Bytes, true
}
