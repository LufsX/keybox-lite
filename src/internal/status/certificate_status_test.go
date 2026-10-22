package status

import "testing"

func TestCertificateStatusPreservesExactSerialAndReason(t *testing.T) {
	data := []byte(`{"entries":{"0015510740886364958753":{"status":"REVOKED","reason":"KEY_COMPROMISE"},"00AB":{"status":"SUSPENDED","reason":"CA_COMPROMISE"}}}`)
	serials := []string{"100664404977750268806996", "000171", "100664404977750268806995", "171"}
	got, err := Check(data, serials)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Serial: "000171", Status: "SUSPENDED", Reason: "CA_COMPROMISE"},
		{Serial: "100664404977750268806995", Status: "REVOKED", Reason: "KEY_COMPROMISE"},
		{Serial: "171", Status: "SUSPENDED", Reason: "CA_COMPROMISE"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v; want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("certificate %d: got %+v; want %+v", i, got[i], want[i])
		}
	}
}

func TestUnknownListStatusRemainsExplicit(t *testing.T) {
	got, err := Check([]byte(`{"entries":{"2a":{"status":"UNKNOWN","reason":"UNSPECIFIED"}}}`), []string{"42"})
	if err != nil || len(got) != 1 || got[0] != (Entry{Serial: "42", Status: "UNKNOWN", Reason: "UNSPECIFIED"}) {
		t.Fatalf("unknown status was lost: %+v, %v", got, err)
	}
}
