// Package status checks certificate serials against Google's attestation status list.
package status

import (
	"encoding/json"
	"fmt"
)

// Entry is a revoked/suspended serial from the list.
type Entry struct {
	Serial string
	Status string
	Reason string
}

// Check returns the entries in the status list that match any of serials.
func Check(data []byte, serials []string) ([]Entry, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("not valid JSON")
	}
	var entries map[string]map[string]interface{}
	if err := json.Unmarshal(root["entries"], &entries); err != nil {
		return nil, fmt.Errorf("no entries")
	}
	var out []Entry
	for _, sn := range serials {
		if e, ok := entries[sn]; ok {
			s, _ := e["status"].(string)
			r, _ := e["reason"].(string)
			out = append(out, Entry{Serial: sn, Status: s, Reason: r})
		}
	}
	return out, nil
}
