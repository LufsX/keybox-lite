// Package keybox parses keybox.xml and extracts certificate serial numbers.
//
// A DER walker reads certificate serials without importing crypto/x509.
package keybox

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// StructureValid checks for a complete XML document with an AndroidAttestation root.
func StructureValid(b []byte) bool {
	_, err := parse(b, false, nil)
	return err == nil
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

// Serials returns decimal serial numbers, or an empty slice if parsing fails.
func Serials(data []byte) []string {
	serials, err := ParseSerials(data)
	if err != nil {
		return []string{}
	}
	return serials
}

// ParseSerials extracts every Certificate element and fails if any cannot be read.
// A partial set must never be used to declare a keybox free of revocations.
func ParseSerials(data []byte) ([]string, error) {
	return parse(data, true, nil)
}

func parse(data []byte, certificates bool, inspect func([]byte) error) ([]string, error) {
	out := []string{}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth, roots := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if roots != 1 || depth != 0 {
				return nil, fmt.Errorf("incomplete keybox XML")
			}
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("keybox XML: %w", err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || token.Name.Local != "AndroidAttestation" {
					return nil, fmt.Errorf("expected AndroidAttestation root")
				}
			}
			if certificates && token.Name.Local == "Certificate" {
				var certificate struct {
					Text     string     `xml:",chardata"`
					Children []struct{} `xml:",any"`
				}
				if err := decoder.DecodeElement(&certificate, &token); err != nil {
					return nil, fmt.Errorf("certificate XML: %w", err)
				}
				der, ok := preparePEM([]byte(certificate.Text), "CERTIFICATE")
				if len(certificate.Children) != 0 || !ok {
					return nil, fmt.Errorf("invalid certificate PEM")
				}
				serial, ok := serialOf(der)
				if !ok {
					return nil, fmt.Errorf("invalid certificate DER")
				}
				if inspect != nil {
					if err := inspect(der); err != nil {
						return nil, err
					}
				}
				out = append(out, bytesToDecimal(serial))
				continue // DecodeElement consumed the closing tag too.
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(token)) != 0 {
				return nil, fmt.Errorf("text outside keybox root")
			}
		}
	}
}

// ---------------------------------------------------------------- DER

// tlv reads one DER element at p, including the offset of the next element.
func tlv(b []byte, p int) (tag byte, val []byte, next int, ok bool) {
	if p < 0 || p >= len(b)-1 {
		return 0, nil, 0, false
	}
	tag = b[p]
	p++
	n := uint64(b[p])
	p++
	if n&0x80 != 0 {
		count := int(n & 0x7f)
		if count == 0 || count > 4 || count > len(b)-p || b[p] == 0 {
			return 0, nil, 0, false
		}
		n = 0
		for i := 0; i < count; i++ {
			n = n<<8 | uint64(b[p])
			p++
		}
		if n < 128 {
			return 0, nil, 0, false
		}
	}
	if n > uint64(len(b)-p) {
		return 0, nil, 0, false
	}
	next = p + int(n)
	return tag, b[p:next], next, true
}

// serialOf walks Certificate -> tbsCertificate -> serialNumber.
func serialOf(der []byte) ([]byte, bool) {
	// Certificate SEQUENCE
	tag, body, next, ok := tlv(der, 0)
	if !ok || tag != 0x30 || next != len(der) {
		return nil, false
	}
	// tbsCertificate SEQUENCE
	tag, tbs, _, ok := tlv(body, 0)
	if !ok || tag != 0x30 {
		return nil, false
	}
	// first field: [0] version (optional), then serialNumber INTEGER
	tag, val, next, ok := tlv(tbs, 0)
	if !ok {
		return nil, false
	}
	if tag == 0xa0 { // explicit version wrapper
		versionTag, version, end, valid := tlv(val, 0)
		if !valid || versionTag != 0x02 || end != len(val) || len(version) != 1 || version[0] > 2 {
			return nil, false
		}
		tag, val, _, ok = tlv(tbs, next)
		if !ok {
			return nil, false
		}
	}
	if tag != 0x02 || len(val) == 0 || val[0]&0x80 != 0 {
		return nil, false
	}
	if len(val) > 1 && val[0] == 0 {
		if val[1]&0x80 == 0 {
			return nil, false
		}
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
