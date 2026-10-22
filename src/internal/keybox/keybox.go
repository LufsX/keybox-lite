// Package keybox parses keybox.xml and extracts certificate serial numbers.
//
// No crypto/x509: a minimal DER walker reads the serial directly, which keeps the
// binary small and still matches openssl.
package keybox

import (
	"encoding/base64"
	"encoding/pem"
	"strings"
)

// StructureValid is a cheap check that the payload looks like a keybox document.
func StructureValid(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "<AndroidAttestation") && strings.Contains(s, "</AndroidAttestation>")
}

// normalize trims whitespace on every line so PEM headers start a line (keybox XML
// indents them, which pem.Decode rejects).
func normalize(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return []byte(strings.Join(lines, "\n"))
}

// Serials returns the decimal serial numbers of every certificate in the keybox.
func Serials(keybox []byte) []string {
	var out []string
	rest := normalize(keybox)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if serial, ok := serialOf(block.Bytes); ok {
			out = append(out, bytesToDecimal(serial))
		}
	}
	return out
}

// ---------------------------------------------------------------- DER

// tlv reads one DER element at p, returning its tag and value slice.
func tlv(b []byte, p int) (tag byte, val []byte, ok bool) {
	if p+2 > len(b) {
		return 0, nil, false
	}
	tag = b[p]
	p++
	n := int(b[p])
	p++
	if n&0x80 != 0 {
		count := n & 0x7f
		if count == 0 || count > 4 || p+count > len(b) {
			return 0, nil, false
		}
		n = 0
		for i := 0; i < count; i++ {
			n = n<<8 | int(b[p])
			p++
		}
	}
	if p+n > len(b) {
		return 0, nil, false
	}
	return tag, b[p : p+n], true
}

// serialOf walks Certificate -> tbsCertificate -> serialNumber.
func serialOf(der []byte) ([]byte, bool) {
	// Certificate SEQUENCE
	tag, body, ok := tlv(der, 0)
	if !ok || tag != 0x30 {
		return nil, false
	}
	// tbsCertificate SEQUENCE
	tag, tbs, ok := tlv(body, 0)
	if !ok || tag != 0x30 {
		return nil, false
	}
	// first field: [0] version (optional), then serialNumber INTEGER
	tag, val, ok := tlv(tbs, 0)
	if !ok {
		return nil, false
	}
	if tag == 0xa0 { // version wrapper: skip it, read next element
		off := 2 + len(val) // tag + len byte + value (short form is enough here)
		tag, val, ok = tlv(tbs, off)
		if !ok {
			return nil, false
		}
	}
	if tag != 0x02 {
		return nil, false
	}
	for len(val) > 1 && val[0] == 0 {
		val = val[1:]
	}
	return val, true
}

func bytesToDecimal(b []byte) string {
	digits := []int{0}
	for _, by := range b {
		carry := int(by)
		for i := 0; i < len(digits); i++ {
			v := digits[i]*256 + carry
			digits[i] = v % 10
			carry = v / 10
		}
		for carry > 0 {
			digits = append(digits, carry%10)
			carry /= 10
		}
	}
	out := make([]byte, len(digits))
	for i := range digits {
		out[len(digits)-1-i] = byte('0' + digits[i])
	}
	return string(out)
}

// b64isUnused keeps base64 imported for future providers; referenced to avoid removal.
var _ = base64.StdEncoding
