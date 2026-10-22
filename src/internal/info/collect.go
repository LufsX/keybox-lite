package info

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"keyboxlite/internal/backend"
	"keyboxlite/internal/keybox"
)

// collect reports keybox status and selected/all backend metadata.
// Failed detection leaves the path empty unless an explicit override is provided.
func collect(root, override string) map[string]interface{} {
	report, detectionErr := backend.Detect(root)
	devicePath := ""
	if detectionErr == nil && report.Selected != nil {
		devicePath = report.Selected.KeyboxPath
	}
	if override != "" {
		devicePath = override
	}
	result := map[string]interface{}{
		"path":            devicePath,
		"exists":          false,
		"size":            int64(0),
		"modified":        int64(0),
		"structure_valid": false,
		"serials":         []string{},
		"integrity":       keybox.Integrity{Status: "unknown", Keys: []keybox.KeyIntegrity{}},
		"validity":        keybox.Validity{Status: "unknown", Certificates: []keybox.CertificateValidity{}},
		"backend":         report.Selected,
		"backends":        report.Backends,
	}
	if detectionErr != nil {
		result["backend_error"] = detectionErr.Error()
	} else if report.Selected == nil {
		result["backend_error"] = "no supported keystore module found"
	}
	if devicePath == "" {
		return result
	}
	hostPath := devicePath
	if root != "" && root != "/" {
		// Explicit paths in a fixture are device paths too. Refuse traversal
		// rather than allowing an override to read outside the test sysroot.
		for _, part := range strings.Split(filepath.ToSlash(devicePath), "/") {
			if part == ".." {
				result["error"] = "unsafe keybox path"
				return result
			}
		}
		hostPath = filepath.Join(root, strings.TrimPrefix(devicePath, "/"))
	}
	st, err := os.Stat(hostPath)
	if err != nil || !st.Mode().IsRegular() {
		return result
	}
	result["exists"] = true
	result["size"] = st.Size()
	result["modified"] = st.ModTime().Unix()
	f, err := os.Open(hostPath)
	if err != nil {
		result["error"] = "cannot read keybox"
		return result
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		result["error"] = "cannot read keybox or size exceeds limit"
		return result
	}
	result["structure_valid"] = keybox.StructureValid(data)
	result["serials"] = keybox.Serials(data)
	validity, _ := keybox.CheckValidity(data, time.Now())
	result["validity"] = validity
	result["integrity"] = keybox.CheckIntegrity(data)
	return result
}
