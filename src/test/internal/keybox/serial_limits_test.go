package keybox

import (
	"bytes"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func serialLimitDER(t *testing.T, serial []byte) []byte {
	t.Helper()
	integer := asn1.RawValue{Tag: 2, Bytes: serial}
	raw, err := asn1.Marshal([]asn1.RawValue{integer})
	if err != nil {
		t.Fatal(err)
	}
	der, err := asn1.Marshal([]asn1.RawValue{{FullBytes: raw}})
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestCertificateSerialOctetLimit(t *testing.T) {
	for _, size := range []int{maxSerialBytes, maxSerialBytes + 1, 1 << 18} {
		der := serialLimitDER(t, bytes.Repeat([]byte{1}, size))
		serial, ok := serialOf(der)
		if ok != (size <= maxSerialBytes) {
			t.Fatalf("serial size %d accepted: %v", size, ok)
		}
		if ok && len(serial) != size {
			t.Fatalf("serial size %d changed", size)
		}
	}
	// The largest unsigned serial is still accepted with its canonical sign byte.
	value := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 160), big.NewInt(1))
	data := document(certificatePEM(t, value))
	got, err := ParseSerials(data)
	if err != nil || len(got) != 1 || got[0] != value.String() {
		t.Fatalf("160-bit serial rejected: %v", err)
	}
}

func TestOversizedSerialFailsClosedBeforeDecimalConversion(t *testing.T) {
	valid := certificatePEM(t, big.NewInt(42))
	der := serialLimitDER(t, bytes.Repeat([]byte{1}, 1<<18))
	bad := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	for _, data := range [][]byte{document(bad), document(valid, bad), document(bad, valid)} {
		if len(data) >= 1<<20 || !StructureValid(data) {
			t.Fatal("fixture must be a structurally complete upload under 1 MiB")
		}
		if serials, err := ParseSerials(data); err == nil || len(serials) != 0 {
			t.Fatalf("oversized serial accepted: %v", err)
		}
		if serials := Serials(data); serials == nil || len(serials) != 0 {
			t.Fatal("public serial result must be an empty array")
		}
		validity, err := CheckValidity(data, time.Now())
		if err == nil || validity.Status != "unknown" || len(validity.Certificates) != 0 {
			t.Fatalf("oversized serial produced successful validity: %v", err)
		}
	}
}
