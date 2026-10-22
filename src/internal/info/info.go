// Package info reports the installed keybox as JSON (used by the WebUI).
package info

import (
	"encoding/json"
	"fmt"
	"os"

	"keyboxlite/internal/keybox"
)

var candidates = []struct{ Dir, Path string }{
	{"/data/adb/modules/tricky_store", "/data/adb/tricky_store/keybox.xml"},
	{"/data/adb/modules/teesim", "/data/adb/teesim/keybox.xml"},
	{"/data/adb/modules/oh_my_keymint", "/data/misc/keystore/omk/keybox.xml"},
}

// DetectPath returns the keybox path of the first installed keystore backend.
func DetectPath() string {
	for _, c := range candidates {
		if _, err := os.Stat(c.Dir); err == nil {
			return c.Path
		}
	}
	return candidates[0].Path
}

// Run prints a JSON status object and returns an exit code.
func Run(args []string) int {
	path := DetectPath()
	if len(args) > 0 && args[0] != "" {
		path = args[0]
	}
	result := map[string]interface{}{
		"path":            path,
		"exists":          false,
		"size":            0,
		"modified":        0,
		"structure_valid": false,
		"serials":         []string{},
	}
	if st, err := os.Stat(path); err == nil {
		result["exists"] = true
		result["size"] = st.Size()
		result["modified"] = st.ModTime().Unix()
		if data, err := os.ReadFile(path); err == nil {
			result["structure_valid"] = keybox.StructureValid(data)
			result["serials"] = keybox.Serials(data)
		}
	}
	out, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(string(out))
	return 0
}
