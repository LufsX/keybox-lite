package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeCatalog(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCandidatesKeepPreferenceAndExcludeHints(t *testing.T) {
	path := writeCatalog(t, `{
		"entries":[
			{"source":"soft","version":"1","softbanned":true},
			{"source":"revoked","version":"1","revoked":true},
			{"source":"clean","version":"2"},
			{"source":"soft","version":"1"},
			{"source":"clean","version":"2"},
			{"source":"other","version":"3","softbanned":true}],
		"autoOverride":{"source":"revoked","version":"1"},
		"working":{"source":"revoked","version":"1"}
	}`)
	got, err := URLs([]string{path, "https://example.test/key/"})
	want := []string{
		"https://example.test/key/clean/2",
		"https://example.test/key/soft/1",
		"https://example.test/key/other/3",
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("URLs = %v, %v; want %v", got, err, want)
	}
}

func TestOversizedCatalog(t *testing.T) {
	path := writeCatalog(t, strings.Repeat(" ", MaxCatalogBytes+1))
	if _, err := URLs([]string{path, "https://example.test/key"}); err == nil {
		t.Fatal("oversized catalog accepted")
	}
}

func quotedJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestCandidatePathAndBaseValidation(t *testing.T) {
	for _, source := range []string{"", ".", "..", "../escape", "a/b", "a\\b", "%2f", "a?b", "a#b", "a b", "a\nb"} {
		t.Run(source, func(t *testing.T) {
			// Marshal through Go's JSON encoder so newline tests cannot break the fixture.
			path := writeCatalog(t, "{\"entries\":[{\"source\":"+quotedJSON(source)+",\"version\":\"1\"}]}")
			if _, err := URLs([]string{path, "https://example.test/key"}); err == nil {
				t.Fatalf("unsafe source %q accepted", source)
			}
		})
	}
	path := writeCatalog(t, `{"entries":[{"source":"@source","version":"v1"}]}`)
	for _, base := range []string{"file:///tmp", "https://", "https://user:pass@example.test", "https://example.test/?x=1", "https://example.test/#fragment"} {
		if _, err := URLs([]string{path, base}); err == nil {
			t.Fatalf("unsafe base %q accepted", base)
		}
	}
	if _, err := URLs([]string{path, "https://example.test/key"}); err != nil {
		t.Fatalf("valid source name rejected: %v", err)
	}
}
