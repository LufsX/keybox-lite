package keybox

import (
	"fmt"
	"time"
)

// Validity describes time validity only, not trust, signatures or revocation.
type Validity struct {
	Status       string                `json:"status"`
	Certificates []CertificateValidity `json:"certificates"`
}
type CertificateValidity struct {
	Serial    string `json:"serial"`
	NotBefore int64  `json:"not_before"`
	NotAfter  int64  `json:"not_after"`
	Status    string `json:"status"`
}

// CheckValidity checks every certificate at one instant. Endpoints are inclusive.
// Parsing failures never return partial certificate information.
func CheckValidity(data []byte, now time.Time) (Validity, error) {
	result := Validity{Status: "valid", Certificates: []CertificateValidity{}}
	_, err := parse(data, true, func(der []byte) error {
		before, after, err := validityOf(der)
		if err != nil {
			return err
		}
		serial, _ := serialOf(der) // parse validated it before calling us.
		state := "valid"
		if now.Before(before) {
			state = "not_yet_valid"
		}
		if now.After(after) {
			state = "expired"
		}
		if state == "expired" || (state == "not_yet_valid" && result.Status == "valid") {
			result.Status = state
		}
		result.Certificates = append(result.Certificates, CertificateValidity{
			Serial: bytesToDecimal(serial), NotBefore: before.Unix(), NotAfter: after.Unix(), Status: state,
		})
		return nil
	})
	if err == nil && len(result.Certificates) == 0 {
		err = fmt.Errorf("no certificates")
	}
	if err != nil {
		return Validity{Status: "unknown", Certificates: []CertificateValidity{}}, err
	}
	return result, nil
}

// Walk Certificate -> TBSCertificate -> validity without pulling crypto/x509
// into the runtime binary. This does not verify the certificate's signature.
func validityOf(der []byte) (time.Time, time.Time, error) {
	bad := func() (time.Time, time.Time, error) {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid certificate validity")
	}
	tag, body, end, ok := tlv(der, 0)
	if !ok || tag != 0x30 || end != len(der) {
		return bad()
	}
	tag, tbs, _, ok := tlv(body, 0)
	if !ok || tag != 0x30 {
		return bad()
	}
	tag, _, pos, ok := tlv(tbs, 0)
	if !ok {
		return bad()
	}
	if tag == 0xa0 {
		tag, _, pos, ok = tlv(tbs, pos)
		if !ok {
			return bad()
		}
	}
	if tag != 0x02 {
		return bad()
	}
	// signature AlgorithmIdentifier, issuer Name, then validity.
	for i := 0; i < 2; i++ {
		tag, _, pos, ok = tlv(tbs, pos)
		if !ok || tag != 0x30 {
			return bad()
		}
	}
	tag, dates, _, ok := tlv(tbs, pos)
	if !ok || tag != 0x30 {
		return bad()
	}
	tag, raw, pos, ok := tlv(dates, 0)
	if !ok {
		return bad()
	}
	before, err := certificateTime(tag, raw)
	if err != nil {
		return bad()
	}
	tag, raw, end, ok = tlv(dates, pos)
	if !ok || end != len(dates) {
		return bad()
	}
	after, err := certificateTime(tag, raw)
	if err != nil || after.Before(before) {
		return bad()
	}
	return before, after, nil
}

func certificateTime(tag byte, raw []byte) (time.Time, error) {
	// RFC 5280 requires seconds and UTC Z. UTCTime maps 50..99 to
	// 1950..1999 (Go's two-digit-year parser uses a different pivot).
	value := string(raw)
	if tag == 0x17 && len(value) == 13 {
		if value[:2] >= "50" {
			value = "19" + value
		} else {
			value = "20" + value
		}
	} else if tag != 0x18 || len(value) != 15 {
		return time.Time{}, fmt.Errorf("invalid certificate time")
	}
	for _, c := range value[:14] {
		if c < '0' || c > '9' {
			return time.Time{}, fmt.Errorf("invalid certificate time")
		}
	}
	return time.Parse("20060102150405Z", value)
}
