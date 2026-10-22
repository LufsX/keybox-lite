// Package backend detects supported Android keystore modules without invoking them.
// Device paths in the result are independent of the optional host test sysroot.
package backend

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

type Kind string

const (
	TrickyStore       Kind = "tricky-store"
	TrickyStoreLegacy Kind = "tricky-store-legacy"
	OhMyKeymint       Kind = "oh-my-keymint"
	TeeSimulator      Kind = "tee-simulator"
)

// Target describes one installed module and the file its default keybox uses.
// ConfigError leaves KeyboxPath empty: callers must not fall back to a guessed file.
type Target struct {
	Backend     Kind    `json:"backend"`
	Identity    string  `json:"identity"`
	ModuleID    string  `json:"module_id"`
	ModuleDir   string  `json:"module_dir"`
	Name        string  `json:"name,omitempty"`
	Version     string  `json:"version,omitempty"`
	VersionCode *uint64 `json:"version_code,omitempty"`
	Active      bool    `json:"active"`
	ConfigDir   string  `json:"config_dir"`
	ConfigPath  string  `json:"config_path"`
	KeyboxPath  string  `json:"keybox_path"`
	InstallMode string  `json:"install_mode"`
	NewFileMode string  `json:"new_file_mode"`
	// Reload describes the integration behavior: no service is started or restarted.
	// Duck's installer also only publishes the file; device reload needs device testing.
	Reload      string `json:"reload"`
	ConfigError string `json:"config_error,omitempty"`
}

type Report struct {
	Backends []Target `json:"backends"`
	Selected *Target  `json:"selected"`
}

// Detect prefers active Tricky Store, then OhMyKeymint, then TEESimulator. If
// all installed modules are disabled or pending removal, the first installed
// module is selected, matching Duck's detection. A module staged only under
// modules_update is ignored; an installed module's update flag does not disable it.
func Detect(root string) (Report, error) {
	report := Report{Backends: []Target{}}
	if root == "" {
		root = "/"
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return report, fmt.Errorf("sysroot: %w", err)
	}
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		return report, fmt.Errorf("sysroot is not a directory")
	}
	for _, id := range []string{"tricky_store", "oh_my_keymint", "teesim"} {
		moduleDir := "/data/adb/modules/" + id
		raw, err := readOptional(root, moduleDir+"/module.prop", 64*1024)
		if err != nil {
			return report, fmt.Errorf("%s module.prop: %w", id, err)
		}
		if raw == nil {
			continue
		}
		props := properties(string(raw))
		target := Target{
			ModuleID: id, ModuleDir: moduleDir, Name: props["name"], Version: props["version"],
			InstallMode: "atomic-replace", NewFileMode: "0644", Reload: "backend-managed",
		}
		if code, err := strconv.ParseUint(props["versionCode"], 10, 64); err == nil {
			target.VersionCode = &code
		}
		active := true
		for _, flag := range []string{"disable", "remove"} {
			_, err := os.Lstat(hostPath(root, moduleDir+"/"+flag))
			if err == nil {
				active = false
			} else if !os.IsNotExist(err) {
				return report, fmt.Errorf("%s status: %w", id, err)
			}
		}
		target.Active = active
		switch id {
		case "tricky_store":
			target.Backend, target.Identity = TrickyStoreLegacy, "TS-L"
			target.ConfigDir = "/data/adb/tricky_store"
			target.ConfigPath = target.ConfigDir + "/target.txt"
			if target.VersionCode != nil && *target.VersionCode >= 246 {
				target.Backend, target.Identity = TrickyStore, "TS"
				target.ConfigPath = target.ConfigDir + "/config.ini"
			}
		case "oh_my_keymint":
			target.Backend, target.Identity = OhMyKeymint, "OMK"
			target.ConfigDir = "/data/misc/keystore/omk"
			target.ConfigPath = target.ConfigDir + "/config.toml"
		case "teesim":
			target.Backend, target.Identity = TeeSimulator, "TEES"
			target.ConfigDir = "/data/adb/teesim"
			target.ConfigPath = target.ConfigDir + "/config.json"
		}
		target.KeyboxPath = target.ConfigDir + "/keybox.xml"
		if target.Backend == TeeSimulator {
			target.KeyboxPath, err = teeKeyboxPath(root, target)
		}
		if err == nil {
			err = checkPath(root, target.KeyboxPath, true)
		}
		if err != nil {
			target.ConfigError = err.Error()
			target.KeyboxPath = ""
		}
		report.Backends = append(report.Backends, target)
	}
	for i := range report.Backends {
		if report.Selected == nil || report.Backends[i].Active {
			report.Selected = &report.Backends[i]
			if report.Selected.Active {
				break
			}
		}
	}
	if report.Selected != nil && report.Selected.ConfigError != "" {
		return report, fmt.Errorf("%s: %s", report.Selected.Backend, report.Selected.ConfigError)
	}
	return report, nil
}

func properties(raw string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			result[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return result
}

func teeKeyboxPath(root string, target Target) (string, error) {
	raw, err := readOptional(root, target.ConfigPath, 1024*1024)
	if err != nil {
		return "", fmt.Errorf("TEESimulator config.json: %w", err)
	}
	if raw == nil {
		return target.KeyboxPath, nil
	}
	var config struct {
		Profiles map[string]json.RawMessage `json:"profiles"`
	}
	if err := json.Unmarshal(raw, &config); err != nil || strings.TrimSpace(string(raw)) == "null" {
		return "", fmt.Errorf("invalid TEESimulator config.json")
	}
	profile, ok := config.Profiles["default"]
	if !ok {
		return target.KeyboxPath, nil
	}
	var def map[string]json.RawMessage
	if err := json.Unmarshal(profile, &def); err != nil || def == nil {
		return "", fmt.Errorf("invalid TEESimulator default profile")
	}
	value, ok := def["keybox"]
	if !ok {
		return target.KeyboxPath, nil
	}
	var name string
	if err := json.Unmarshal(value, &name); err != nil || !safeRelative(name) {
		return "", fmt.Errorf("unsafe TEESimulator default keybox path")
	}
	return path.Join(target.ConfigDir, name), nil
}

func safeRelative(name string) bool {
	if name == "" || path.IsAbs(name) || strings.Contains(name, "\\") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func hostPath(root, devicePath string) string {
	return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(devicePath, "/")))
}

// checkPath refuses symlinks in every existing component, including a missing
// target's parents. Detection is read-only; the installer must recheck at write time.
func checkPath(root, devicePath string, file bool) error {
	if !strings.HasPrefix(devicePath, "/") || !safeRelative(strings.TrimPrefix(devicePath, "/")) {
		return fmt.Errorf("unsafe device path")
	}
	parts := strings.Split(strings.TrimPrefix(devicePath, "/"), "/")
	current := root
	for i, part := range parts {
		current = filepath.Join(current, part)
		st, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("cannot inspect %s", devicePath)
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in %s", devicePath)
		}
		if i < len(parts)-1 && !st.IsDir() {
			return fmt.Errorf("non-directory parent in %s", devicePath)
		}
		if i == len(parts)-1 && file && !st.Mode().IsRegular() {
			return fmt.Errorf("not a regular file: %s", devicePath)
		}
	}
	return nil
}

func readOptional(root, devicePath string, limit int64) ([]byte, error) {
	if err := checkPath(root, devicePath, true); err != nil {
		return nil, err
	}
	f, err := os.Open(hostPath(root, devicePath))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds size limit")
	}
	return data, nil
}
