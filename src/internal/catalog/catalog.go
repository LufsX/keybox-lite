// Package catalog reads the dpejoh keybox catalog and picks an eligible entry.
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func boolean(m map[string]interface{}, key string) bool {
	b, _ := m[key].(bool)
	return b
}

func str(m map[string]interface{}, key string) string {
	s, _ := m[key].(string)
	return s
}

// PickURL reads args = [catalog.json, base-url] and returns the payload URL.
func PickURL(args []string) (url, src, ver string, err error) {
	if len(args) < 2 {
		return "", "", "", fmt.Errorf("spectre-url needs <catalog.json> <base-url>")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return "", "", "", err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return "", "", "", err
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(root["entries"], &entries); err != nil {
		return "", "", "", err
	}
	pick := -1
	for i, e := range entries {
		if !boolean(e, "revoked") && !boolean(e, "softbanned") {
			pick = i
			break
		}
	}
	if pick < 0 {
		for i, e := range entries {
			if !boolean(e, "revoked") {
				pick = i
				break
			}
		}
	}
	if pick < 0 {
		return "", "", "", fmt.Errorf("no eligible catalog entry")
	}
	src = str(entries[pick], "source")
	ver = str(entries[pick], "version")
	if src == "" || ver == "" {
		return "", "", "", fmt.Errorf("selected entry missing source/version")
	}
	url = strings.TrimRight(args[1], "/") + "/" + src + "/" + ver
	return url, src, ver, nil
}
