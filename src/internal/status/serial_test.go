package status

import "testing"

func TestGoogleHexSerialRegression(t *testing.T) {
	// Decimal 100664404977750268806995 is hexadecimal 15510740886364958753.
	data := []byte(`{"entries":{"15510740886364958753":{"status":"REVOKED","reason":"KEY_COMPROMISE"}}}`)
	got, err := Check(data, []string{"100664404977750268806995"})
	if err != nil || len(got) != 1 || got[0].Status != "REVOKED" || got[0].Reason != "KEY_COMPROMISE" {
		t.Fatalf("missed reported revoked serial: %+v %v", got, err)
	}
	// Never guess the base from whether a key contains a-f or also try decimal:
	// that would flag a different certificate with a coincidentally matching string.
	if got, err := Check(data, []string{"15510740886364958753"}); err != nil || len(got) != 0 {
		t.Fatalf("false positive from decimal/hex ambiguity: %+v %v", got, err)
	}
	for _, hex := range []string{"ab", "AB", "00aB"} {
		data := []byte(`{"entries":{"` + hex + `":{"status":"SUSPENDED"}}}`)
		got, err := Check(data, []string{"171"})
		if err != nil || len(got) != 1 {
			t.Fatalf("hex %s: %+v %v", hex, got, err)
		}
	}
}

func TestInvalidOrConflictingSerials(t *testing.T) {
	for _, data := range []string{
		`{"entries":{"xyz":{"status":"REVOKED"}}}`,
		`{"entries":{"":{"status":"REVOKED"}}}`,
		`{"entries":{"0x2a":{"status":"REVOKED"}}}`,
		`{"entries":{"2a":{"status":"REVOKED"},"002A":{"status":"SUSPENDED"}}}`,
	} {
		if got, err := Check([]byte(data), nil); err == nil {
			t.Fatalf("accepted %s: %+v", data, got)
		}
	}
	for _, sn := range []string{"", "-1", "+42", "2a", " 42"} {
		if _, err := Check([]byte(`{"entries":{}}`), []string{sn}); err == nil {
			t.Fatalf("accepted decimal %q", sn)
		}
	}
}
