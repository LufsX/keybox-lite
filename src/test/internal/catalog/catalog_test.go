package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPickURL(t *testing.T) {
	for _, tc := range []struct {
		name, entries, source string
		wantErr               bool
	}{
		{"prefer clean", `[{"source":"banned","version":"1","softbanned":true},{"source":"clean","version":"2","revoked":false}]`, "clean", false},
		{"softban fallback", `[{"source":"revoked","version":"1","revoked":true},{"source":"fallback","version":"2","softbanned":true}]`, "fallback", false},
		{"optional flags", `[{"source":"clean","version":"2"}]`, "clean", false},
		{"all revoked", `[{"source":"x","version":"2","revoked":true}]`, "", true},
		{"empty", `[]`, "", true},
		{"null entries", `null`, "", true},
		{"null row", `[null,{"source":"clean","version":"2"}]`, "", true},
		{"string revoked", `[{"source":"x","version":"2","revoked":"true"}]`, "", true},
		{"null revoked", `[{"source":"x","version":"2","revoked":null}]`, "", true},
		{"number softbanned", `[{"source":"x","version":"2","softbanned":1}]`, "", true},
		{"missing version", `[{"source":"x"}]`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "catalog.json")
			if err := os.WriteFile(path, []byte(`{"entries":`+tc.entries+`}`), 0600); err != nil {
				t.Fatal(err)
			}
			url, source, version, err := PickURL([]string{path, "https://example.test/key///"})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("selected malformed/revoked entry: %s", url)
				}
				return
			}
			if err != nil || source != tc.source || version != "2" || url != "https://example.test/key/"+tc.source+"/2" {
				t.Fatalf("PickURL = %q, %q, %q, %v", url, source, version, err)
			}
		})
	}
}
