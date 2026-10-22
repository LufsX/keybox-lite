// Package catalog reads the dpejoh keybox catalog and picks an eligible entry.
package catalog

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"unicode"
)

// MaxCatalogBytes bounds downloaded directory metadata before JSON parsing.
const MaxCatalogBytes = 1024 * 1024

func boolean(m map[string]interface{}, key string) bool {
	b, _ := m[key].(bool)
	return b
}

func str(m map[string]interface{}, key string) string {
	s, _ := m[key].(string)
	return s
}

// Candidate is one non-revoked catalog entry. Catalog flags are hints only;
// downloaded keyboxes still require full certificate and revocation validation.
type Candidate struct {
	URL     string
	Source  string
	Version string
}

// PickURL reads args = [catalog.json, base-url] and returns the first eligible URL.
func PickURL(args []string) (url, src, ver string, err error) {
	candidates, err := Candidates(args)
	if err != nil {
		return "", "", "", err
	}
	c := candidates[0]
	return c.URL, c.Source, c.Version, nil
}

// URLs returns candidates in preference order for download/decode fallback.
func URLs(args []string) ([]string, error) {
	candidates, err := Candidates(args)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(candidates))
	for i, c := range candidates {
		out[i] = c.URL
	}
	return out, nil
}

// Candidates preserves catalog order within clean and softbanned tiers.
// Revoked entries and the catalog's unverified autoOverride/working hints are
// never selected. Malformed flags or unsafe eligible paths fail closed.
func Candidates(args []string) ([]Candidate, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("spectre-url needs <catalog.json> <base-url>")
	}
	base, err := url.Parse(strings.TrimRight(args[1], "/"))
	if err != nil || base == nil || (base.Scheme != "https" && base.Scheme != "http") ||
		base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" {
		return nil, fmt.Errorf("catalog base must be an http(s) URL without credentials, query or fragment")
	}
	file, err := os.Open(args[0])
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxCatalogBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxCatalogBytes {
		return nil, fmt.Errorf("catalog exceeds %d bytes", MaxCatalogBytes)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(root["entries"], &entries); err != nil {
		return nil, err
	}
	for i, e := range entries {
		if e == nil {
			return nil, fmt.Errorf("catalog entry %d is null", i)
		}
		for _, key := range []string{"revoked", "softbanned"} {
			if value, exists := e[key]; exists {
				if _, ok := value.(bool); !ok {
					return nil, fmt.Errorf("catalog entry %d: %s must be a boolean", i, key)
				}
			}
		}
	}
	var clean, soft []Candidate
	for i, e := range entries {
		if boolean(e, "revoked") {
			continue
		}
		src, ver := str(e, "source"), str(e, "version")
		if !safeSegment(src) || !safeSegment(ver) {
			return nil, fmt.Errorf("catalog entry %d has unsafe or missing source/version", i)
		}
		payloadURL := base.String() + "/" + url.PathEscape(src) + "/" + url.PathEscape(ver)
		c := Candidate{URL: payloadURL, Source: src, Version: ver}
		if boolean(e, "softbanned") {
			soft = append(soft, c)
		} else {
			clean = append(clean, c)
		}
	}
	var out []Candidate
	seen := make(map[string]bool)
	for _, c := range append(clean, soft...) {
		if !seen[c.URL] {
			seen[c.URL] = true
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no eligible catalog entry")
	}
	return out, nil
}

func safeSegment(s string) bool {
	if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\%?#") {
		return false
	}
	for _, c := range s {
		if unicode.IsSpace(c) || unicode.IsControl(c) {
			return false
		}
	}
	return true
}
