// Package targets manages Tricky Store targets and optional backend WebUI entries.
// The config formats and auto-target behavior follow Duck-ToolBox (MIT).
package targets

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"keyboxlite/internal/backend"
)

const stateDir = "/data/adb/keybox-lite/targets"
const statePath = stateDir + "/state.json"
const maxIO = 1024 * 1024

type Target struct {
	PackageName string `json:"package_name"`
	Mode        string `json:"mode"`
}
type Policy = map[string]string
type Config struct {
	Targets       []Target          `json:"targets"`
	DefaultPolicy Policy            `json:"default_policy"`
	PerAppPolicy  map[string]Policy `json:"per_app_policy"`
}
type Package struct {
	PackageName string `json:"package_name"`
	System      bool   `json:"system"`
	Selected    bool   `json:"selected"`
	Mode        string `json:"mode"`
	Installed   bool   `json:"installed"`
}
type Entry struct {
	Enabled     bool    `json:"enabled"`
	ModuleID    *string `json:"module_id"`
	Linked      bool    `json:"linked"`
	HasOwnWebUI bool    `json:"has_own_webui"`
}
type Status struct {
	Supported      bool            `json:"supported"`
	Backend        *backend.Target `json:"backend"`
	Config         Config          `json:"config"`
	Packages       []Package       `json:"packages"`
	SystemApps     []string        `json:"system_apps"`
	AutoAddNewApps bool            `json:"auto_add_new_apps"`
	Entry          Entry           `json:"entry"`
	Revision       string          `json:"revision"`
	ConfigError    string          `json:"config_error,omitempty"`
	PackagesError  string          `json:"packages_error,omitempty"`
}
type SaveRequest struct {
	Revision       string            `json:"revision"`
	Targets        []Target          `json:"targets"`
	DefaultPolicy  Policy            `json:"default_policy"`
	PerAppPolicy   map[string]Policy `json:"per_app_policy"`
	SystemApps     []string          `json:"system_apps"`
	AutoAddNewApps bool              `json:"auto_add_new_apps"`
}
type state struct {
	Version    int      `json:"version"`
	SystemApps []string `json:"system_apps"`
	Auto       bool     `json:"auto_add_new_apps"`
	Baseline   bool     `json:"baseline_initialized"`
	Known      []string `json:"known_user_apps"`
	Entry      bool     `json:"entry_enabled"`
}

func emptyConfig() Config { return Config{[]Target{}, Policy{}, map[string]Policy{}} }
func defaultState() state {
	return state{Version: 1, SystemApps: []string{"com.google.android.gms", "com.android.vending", "com.oplus.deepthinker", "com.heytap.speechassist", "com.coloros.sceneservice"}, Known: []string{}}
}

func isUID(s string) bool { return strings.HasPrefix(s, "uid:") }
func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func packageName(s string) bool {
	for _, part := range strings.Split(s, ".") {
		if part == "" {
			return false
		}
		for i, c := range part {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || (i > 0 && c >= '0' && c <= '9') {
				continue
			}
			return false
		}
	}
	return true
}

func validName(s string) bool {
	if len(s) > 255 {
		return false
	}
	if isUID(s) {
		if !digits(s[4:]) {
			return false
		}
		_, err := strconv.ParseUint(s[4:], 10, 32)
		return err == nil
	}
	base, user, hasUser := strings.Cut(s, "@")
	if hasUser {
		if !digits(user) {
			return false
		}
		if _, err := strconv.ParseUint(user, 10, 32); err != nil {
			return false
		}
	}
	return packageName(base) && base != "target" && base != "default_policy" && base != "constructor" && base != "prototype" && base != "__proto__"
}
func modeMarker(m string) string {
	if m == "generate" {
		return "!"
	}
	if m == "hack" {
		return "?"
	}
	return ""
}
func knownKey(k string) bool { return k == "os_patch" || k == "vendor_patch" || k == "boot_patch" }
func validPatch(k, v string) bool {
	if v == "prop" || v == "no" {
		return true
	}
	layout := "20060102"
	if k == "os_patch" {
		layout = "200601"
	}
	if len(v) != len(layout) || v[:4] < "1900" {
		return false
	}
	_, err := time.Parse(layout, v)
	return err == nil
}
func copyEditable(p Policy) Policy {
	out := Policy{}
	for k, v := range p {
		if v != "" {
			out[k] = v
		}
	}
	return out
}
func validatePolicy(p Policy) error {
	for k, v := range p {
		if !knownKey(k) || !validPatch(k, v) {
			return fmt.Errorf("invalid patch policy %q=%q", k, v)
		}
	}
	return nil
}
func validateConfig(c *Config) error {
	seen := map[string]bool{}
	for i := range c.Targets {
		t := &c.Targets[i]
		if t.Mode == "" {
			t.Mode = "auto"
		}
		if !validName(t.PackageName) || (t.Mode != "auto" && t.Mode != "generate" && t.Mode != "hack") || seen[t.PackageName] {
			return fmt.Errorf("invalid or duplicate target %q", t.PackageName)
		}
		seen[t.PackageName] = true
	}
	if err := validatePolicy(c.DefaultPolicy); err != nil {
		return err
	}
	for p, v := range c.PerAppPolicy {
		if !validName(p) {
			return fmt.Errorf("invalid policy package %q", p)
		}
		if err := validatePolicy(v); err != nil {
			return err
		}
	}
	sort.Slice(c.Targets, func(i, j int) bool { return c.Targets[i].PackageName < c.Targets[j].PackageName })
	return nil
}
func validateNames(names []string) error {
	seen := map[string]bool{}
	for _, p := range names {
		if !validName(p) || isUID(p) || seen[p] {
			return fmt.Errorf("invalid or duplicate package %q", p)
		}
		seen[p] = true
	}
	sort.Strings(names)
	return nil
}
