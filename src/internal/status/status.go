// Package status checks certificate serials against Google's attestation status list.
package status

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// Entry is a revoked/suspended serial from the list.
type Entry struct {
	Serial string
	Status string
	Reason string
}

// Check accepts decimal certificate serials from keybox.ParseSerials.
// Google status-list keys are hexadecimal, even when they contain only digits.
func Check(data []byte, serials []string) ([]Entry, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("not valid JSON")
	}
	var entries map[string]struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(root["entries"], &entries); err != nil || entries == nil {
		return nil, fmt.Errorf("no entries")
	}
	normalized := make(map[string]string, len(entries))
	for sn, e := range entries {
		if e.Status == "" {
			return nil, fmt.Errorf("entry %q missing status", sn)
		}
		hex, err := normalizeHex(sn)
		if err != nil {
			return nil, err
		}
		if previous, exists := normalized[hex]; exists && entries[previous] != e {
			return nil, fmt.Errorf("conflicting status entries for serial %q", sn)
		}
		normalized[hex] = sn
	}
	var out []Entry
	for _, sn := range serials {
		if sn == "" || strings.Trim(sn, "0123456789") != "" {
			return nil, fmt.Errorf("invalid decimal certificate serial %q", sn)
		}
		n, ok := new(big.Int).SetString(sn, 10)
		if !ok {
			return nil, fmt.Errorf("invalid decimal certificate serial %q", sn)
		}
		if original, ok := normalized[n.Text(16)]; ok {
			e := entries[original]
			out = append(out, Entry{Serial: sn, Status: e.Status, Reason: e.Reason})
		}
	}
	return out, nil
}

func normalizeHex(serial string) (string, error) {
	if serial == "" || strings.Trim(serial, "0123456789abcdefABCDEF") != "" {
		return "", fmt.Errorf("invalid hexadecimal status serial %q", serial)
	}
	serial = strings.TrimLeft(strings.ToLower(serial), "0")
	if serial == "" {
		serial = "0"
	}
	return serial, nil
}
