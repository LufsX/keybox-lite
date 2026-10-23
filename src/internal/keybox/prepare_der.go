package keybox

import (
	"bytes"
	"crypto/elliptic"
	"math/big"
)

// These readers check DER shape, supported encodings and EC private-key points.
// RSA factors, signature arithmetic and key/certificate correspondence are not checked.
type prepareDER struct {
	data []byte
	pos  int
}

func (r *prepareDER) take(want byte) ([]byte, bool) {
	tag, value, end, ok := tlv(r.data, r.pos)
	if !ok || tag != want {
		return nil, false
	}
	r.pos = end
	return value, true
}

func (r *prepareDER) done() bool { return r.pos == len(r.data) }

func prepareSequence(data []byte) ([]byte, bool) {
	r := prepareDER{data: data}
	body, ok := r.take(0x30)
	return body, ok && r.done()
}

func prepareInteger(r *prepareDER, zero bool) ([]byte, bool) {
	n, ok := r.take(2)
	if !ok || len(n) == 0 || n[0]&0x80 != 0 || (len(n) > 1 && n[0] == 0 && n[1]&0x80 == 0) {
		return nil, false
	}
	if !zero && bytes.Equal(n, []byte{0}) {
		return nil, false
	}
	return n, true
}

func prepareOID(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	start := true
	for _, v := range b {
		if start && v == 0x80 {
			return false
		}
		start = v&0x80 == 0
	}
	return start
}

const (
	prepareRSAOID = "\x2a\x86\x48\x86\xf7\x0d\x01\x01\x01"
	prepareECOID  = "\x2a\x86\x48\xce\x3d\x02\x01"
)

// prepareAlgorithm returns the key algorithm and EC scalar width.
func prepareAlgorithm(body []byte) (string, int, bool) {
	r := prepareDER{data: body}
	oid, ok := r.take(6)
	if !ok {
		return "", 0, false
	}
	switch string(oid) {
	case prepareRSAOID:
		if !r.done() {
			null, valid := r.take(5)
			if !valid || len(null) != 0 || !r.done() {
				return "", 0, false
			}
		}
		return "rsa", 0, true
	case prepareECOID:
		curve, valid := r.take(6)
		width := prepareCurve(curve)
		return "ec", width, valid && width != 0 && r.done()
	}
	return "", 0, false
}

func prepareCurve(oid []byte) int {
	switch string(oid) {
	case "\x2b\x81\x04\x00\x21": // secp224r1
		return 28
	case "\x2a\x86\x48\xce\x3d\x03\x01\x07": // prime256v1
		return 32
	case "\x2b\x81\x04\x00\x22": // secp384r1
		return 48
	case "\x2b\x81\x04\x00\x23": // secp521r1
		return 66
	}
	return 0
}

func preparePrivate(text []byte, algorithm string) bool {
	block := singlePEM(text)
	if block == nil {
		return false
	}
	body, ok := prepareSequence(block.Bytes)
	if !ok {
		return false
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return algorithm == "rsa" && prepareRSA(body, true)
	case "EC PRIVATE KEY":
		return algorithm == "ec" && prepareEC(body, 0)
	case "PRIVATE KEY": // unencrypted PKCS#8 PrivateKeyInfo
		r := prepareDER{data: body}
		version, valid := prepareInteger(&r, true)
		if !valid || !bytes.Equal(version, []byte{0}) {
			return false
		}
		identifier, valid := r.take(0x30)
		algo, width, supported := prepareAlgorithm(identifier)
		if !valid || !supported || algo != algorithm {
			return false
		}
		inner, valid := r.take(4)
		if !valid || !r.done() {
			return false
		}
		innerBody, valid := prepareSequence(inner)
		if !valid {
			return false
		}
		if algo == "rsa" {
			return prepareRSA(innerBody, true)
		}
		return prepareEC(innerBody, width)
	default:
		return false
	}
}

func prepareRSA(body []byte, private bool) bool {
	r := prepareDER{data: body}
	if private {
		version, ok := prepareInteger(&r, true)
		if !ok || !bytes.Equal(version, []byte{0}) {
			return false // only the common two-prime PKCS#1 form
		}
	}
	modulus, ok := prepareInteger(&r, false)
	if !ok || modulus[len(modulus)-1]&1 == 0 {
		return false
	}
	exponent, ok := prepareInteger(&r, false)
	if !ok || len(exponent) > 8 || exponent[len(exponent)-1]&1 == 0 || (len(exponent) == 1 && exponent[0] < 3) {
		return false
	}
	if private {
		for i := 0; i < 6; i++ { // d, p, q, dP, dQ, qInv
			if _, ok := prepareInteger(&r, false); !ok {
				return false
			}
		}
	}
	return r.done()
}

func prepareEC(body []byte, outerWidth int) bool {
	r := prepareDER{data: body}
	version, ok := prepareInteger(&r, true)
	if !ok || !bytes.Equal(version, []byte{1}) {
		return false
	}
	scalar, ok := r.take(4)
	if !ok || len(scalar) == 0 {
		return false
	}
	width := outerWidth
	tag, _, _, present := tlv(r.data, r.pos)
	if present && tag == 0xa0 {
		params, _ := r.take(0xa0)
		curve := prepareDER{data: params}
		oid, valid := curve.take(6)
		innerWidth := prepareCurve(oid)
		if !valid || !curve.done() || innerWidth == 0 || (width != 0 && width != innerWidth) {
			return false
		}
		width = innerWidth
	}
	if width == 0 || len(scalar) != width || bytes.Equal(scalar, make([]byte, width)) || (width == 66 && scalar[0] > 1) {
		return false
	}
	var curve elliptic.Curve
	switch width {
	case 28:
		curve = elliptic.P224()
	case 32:
		curve = elliptic.P256()
	case 48:
		curve = elliptic.P384()
	case 66:
		curve = elliptic.P521()
	default:
		return false
	}
	if new(big.Int).SetBytes(scalar).Cmp(curve.Params().N) >= 0 {
		return false
	}
	if !r.done() {
		public, valid := r.take(0xa1)
		bits := prepareDER{data: public}
		point, pointOK := bits.take(3)
		if !valid || !pointOK || !bits.done() || !preparePoint(point, width) {
			return false
		}
		x, y := curve.ScalarBaseMult(scalar)
		if !bytes.Equal(point[1:], elliptic.Marshal(curve, x, y)) {
			return false
		}
	}
	return r.done()
}

func preparePoint(bits []byte, width int) bool {
	// SEC1 uncompressed public point, with no unused BIT STRING bits.
	return len(bits) == 2+2*width && bits[0] == 0 && bits[1] == 4
}

func prepareCertificate(der []byte) (string, bool) {
	body, ok := prepareSequence(der)
	if !ok {
		return "", false
	}
	r := prepareDER{data: body}
	tbs, ok := r.take(0x30)
	if !ok {
		return "", false
	}
	signatureAlgorithm, ok := r.take(0x30)
	if !ok || !prepareSignatureAlgorithm(signatureAlgorithm) {
		return "", false
	}
	signature, ok := r.take(3)
	if !ok || len(signature) < 2 || signature[0] != 0 || !r.done() {
		return "", false
	}
	// Check canonical serial and time encoding;
	// no comparison with the current time is made here.
	if _, valid := serialOf(der); !valid {
		return "", false
	}
	if _, _, err := validityOf(der); err != nil {
		return "", false
	}
	t := prepareDER{data: tbs}
	tag, _, _, _ := tlv(t.data, 0)
	if tag == 0xa0 {
		if _, ok := t.take(0xa0); !ok {
			return "", false
		}
	}
	if _, ok := prepareInteger(&t, true); !ok {
		return "", false
	}
	innerAlgorithm, ok := t.take(0x30)
	if !ok || !bytes.Equal(innerAlgorithm, signatureAlgorithm) {
		return "", false
	}
	issuer, ok := t.take(0x30)
	if !ok || !prepareName(issuer) {
		return "", false
	}
	if _, ok := t.take(0x30); !ok { // validity, already checked above
		return "", false
	}
	subject, ok := t.take(0x30)
	if !ok || !prepareName(subject) {
		return "", false
	}
	spki, ok := t.take(0x30)
	if !ok {
		return "", false
	}
	pub := prepareDER{data: spki}
	identifier, ok := pub.take(0x30)
	algorithm, width, supported := prepareAlgorithm(identifier)
	if !ok || !supported {
		return "", false
	}
	bits, ok := pub.take(3)
	if !ok || len(bits) < 2 || bits[0] != 0 || !pub.done() {
		return "", false
	}
	if algorithm == "ec" {
		if !preparePoint(bits, width) {
			return "", false
		}
	} else {
		key, valid := prepareSequence(bits[1:])
		if !valid || !prepareRSA(key, false) {
			return "", false
		}
	}
	lastTag := byte(0x80)
	for !t.done() {
		tag, value, end, valid := tlv(t.data, t.pos)
		if !valid || tag <= lastTag {
			return "", false
		}
		switch tag {
		case 0x81, 0x82: // optional issuer/subject unique identifier
			if len(value) < 2 || value[0] > 7 || value[len(value)-1]&byte((1<<value[0])-1) != 0 {
				return "", false
			}
		case 0xa3:
			if !prepareExtensions(value) {
				return "", false
			}
		default:
			return "", false
		}
		t.pos, lastTag = end, tag
	}
	return algorithm, true
}

func prepareSignatureAlgorithm(body []byte) bool {
	r := prepareDER{data: body}
	oid, ok := r.take(6)
	if !ok || !prepareOID(oid) {
		return false
	}
	if !r.done() {
		_, value, end, valid := tlv(r.data, r.pos)
		if !valid || !prepareDERTree(value, 0) {
			return false
		}
		r.pos = end
	}
	return r.done()
}

// Walk constructed ASN.1 values; this bounds nesting and rejects truncated
// algorithm parameters without importing the general ASN.1/crypto stack.
func prepareDERTree(body []byte, depth int) bool {
	if depth > 16 {
		return false
	}
	for p := 0; p < len(body); {
		tag, value, end, ok := tlv(body, p)
		if !ok || tag&0x1f == 0x1f || (tag&0x20 != 0 && !prepareDERTree(value, depth+1)) {
			return false
		}
		p = end
	}
	return true
}

func prepareName(body []byte) bool {
	r := prepareDER{data: body}
	for !r.done() {
		set, ok := r.take(0x31)
		if !ok || len(set) == 0 {
			return false
		}
		values := prepareDER{data: set}
		for !values.done() {
			value, valid := values.take(0x30)
			if !valid {
				return false
			}
			attr := prepareDER{data: value}
			oid, valid := attr.take(6)
			if !valid || !prepareOID(oid) {
				return false
			}
			_, _, end, valid := tlv(attr.data, attr.pos)
			if !valid || end != len(attr.data) {
				return false
			}
		}
	}
	return true
}

func prepareExtensions(explicit []byte) bool {
	body, ok := prepareSequence(explicit)
	if !ok || len(body) == 0 {
		return false
	}
	r := prepareDER{data: body}
	seen := map[string]bool{}
	for !r.done() {
		extension, ok := r.take(0x30)
		if !ok {
			return false
		}
		e := prepareDER{data: extension}
		oid, valid := e.take(6)
		if !valid || !prepareOID(oid) || seen[string(oid)] {
			return false
		}
		seen[string(oid)] = true
		tag, _, _, _ := tlv(e.data, e.pos)
		if tag == 1 {
			critical, valid := e.take(1)
			if !valid || len(critical) != 1 || critical[0] != 0xff {
				return false
			}
		}
		value, valid := e.take(4)
		// extnValue is opaque here: Android provisioning information
		// (1.3.6.1.4.1.11129.2.1.30) contains CBOR, not an ASN.1 tree.
		// Keep validating the Extension envelope without interpreting its payload.
		if !valid || len(value) == 0 || !e.done() {
			return false
		}
	}
	return true
}
