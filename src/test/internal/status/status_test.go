package status

import "testing"

func TestCheckRejectsInvalidLists(t *testing.T) {
	for _, data := range []string{
		``, `null`, `[]`, `{}`, `{"entries":null}`, `{"entries":[]}`,
		`{"entries":"entries"}`, `{"entries":{"42":null}}`,
		`{"entries":{"42":{}}}`, `{"entries":{"42":{"status":null}}}`,
		`{"entries":{"42":{"status":false}}}`,
		`{"entries":{"42":{"status":"REVOKED","reason":5}}}`,
		`{"entries":{}} trailing`,
	} {
		t.Run(data, func(t *testing.T) {
			// Even malformed entries unrelated to the checked serial must not
			// turn an unusable list into a successful check.
			if got, err := Check([]byte(data), []string{"99"}); err == nil {
				t.Fatalf("Check(%s) = %v, want error", data, got)
			}
		})
	}
}

func TestCheckMatchesRevokedAndSuspended(t *testing.T) {
	data := []byte(`{"entries":{"2a":{"status":"REVOKED","reason":"KEY_COMPROMISE"},"63":{"status":"SUSPENDED"}}}`)
	got, err := Check(data, []string{"99", "7", "42"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (Entry{Serial: "99", Status: "SUSPENDED"}) ||
		got[1] != (Entry{Serial: "42", Status: "REVOKED", Reason: "KEY_COMPROMISE"}) {
		t.Fatalf("unexpected matches: %+v", got)
	}
	for _, serials := range [][]string{nil, {"123"}} {
		if got, err := Check(data, serials); err != nil || len(got) != 0 {
			t.Fatalf("unmatched serials: %v, %v", got, err)
		}
	}
	if got, err := Check([]byte(`{"entries":{}}`), []string{"42"}); err != nil || len(got) != 0 {
		t.Fatalf("empty entries object: %v, %v", got, err)
	}
}
