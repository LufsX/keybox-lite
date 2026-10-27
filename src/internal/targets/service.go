package targets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"keyboxlite/internal/backend"
)

type packageList func() (map[string]bool, error)
type manager struct {
	disk   *disk
	root   string
	module string
	list   packageList
	// Tests inject a failure at the actual write boundary, after earlier writes.
}

func newManager(root, module string) (*manager, error) {
	if root == "" {
		root = "/"
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if module == "" {
		module = defaultModule()
	}
	if !cleanDevice(module) {
		return nil, fmt.Errorf("module-dir must be an absolute clean device path")
	}
	d, err := openDisk(root)
	if err != nil {
		return nil, err
	}
	m := &manager{disk: d, root: root, module: module}
	m.list = func() (map[string]bool, error) { return listPackages(root) }
	return m, nil
}
func defaultModule() string {
	exe, err := os.Executable()
	if err == nil && filepath.Base(filepath.Dir(exe)) == "bin" {
		return filepath.Dir(filepath.Dir(exe))
	}
	return "/data/adb/modules/keybox-lite"
}
func (m *manager) close() { m.disk.root.Close() }
func (m *manager) locked(fn func() error) error {
	lock, err := m.disk.lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = m.disk.recover(); err != nil {
		return err
	}
	return fn()
}
func strictJSON(data []byte, v any) error {
	if len(data) > maxIO {
		return fmt.Errorf("JSON exceeds IO limit")
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("JSON object required")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	var tail any
	if err := dec.Decode(&tail); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}
func (m *manager) loadState() (state, snapshot, error) {
	s, err := m.disk.read(statePath, false)
	if err != nil {
		return state{}, s, err
	}
	st := defaultState()
	if s.Exists {
		st = state{}
		if err = strictJSON(s.Data, &st); err != nil {
			return st, s, fmt.Errorf("state: %w", err)
		}
		if st.Version != 1 {
			return st, s, fmt.Errorf("unsupported targets state version")
		}
		if err = validateNames(st.SystemApps); err != nil {
			return st, s, err
		}
		if err = validateNames(st.Known); err != nil {
			return st, s, err
		}
	} else {
		legacy, e := m.disk.read("/data/adb/tricky_store/system_app", false)
		if e != nil {
			return st, s, e
		}
		set := map[string]bool{}
		for _, p := range st.SystemApps {
			set[p] = true
		}
		for _, p := range strings.Split(string(legacy.Data), "\n") {
			p = strings.TrimSpace(p)
			if contentLine(p) {
				if !validName(p) || isUID(p) {
					return st, s, fmt.Errorf("invalid legacy system_app")
				}
				if !set[p] {
					st.SystemApps = append(st.SystemApps, p)
					set[p] = true
				}
			}
		}
		sort.Strings(st.SystemApps)
	}
	if st.SystemApps == nil {
		st.SystemApps = []string{}
	}
	if st.Known == nil {
		st.Known = []string{}
	}
	return st, s, nil
}
func stateChange(st state, old snapshot) (change, error) {
	data, err := json.Marshal(st)
	return change{statePath, old, fileImage(data, old, true)}, err
}
func parseProps(data []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if !contentLine(line) {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}
func (m *manager) detect() (*backend.Target, []byte, error) {
	var selected *backend.Target
	var identity []byte
	for _, id := range backendIDs {
		dir := "/data/adb/modules/" + id
		prop, err := m.disk.read(dir+"/module.prop", false)
		if err != nil {
			return nil, nil, err
		}
		identity = append(identity, []byte(id)...)
		identity = append(identity, prop.Data...)
		if !prop.Exists {
			continue
		}
		p := parseProps(prop.Data)
		t := backend.Target{ModuleID: id, ModuleDir: dir, Name: p["name"], Version: p["version"], Active: true, InstallMode: "atomic-replace", NewFileMode: "0644", Reload: "backend-managed"}
		if v, err := strconv.ParseUint(p["versionCode"], 10, 64); err == nil {
			t.VersionCode = &v
		}
		for _, flag := range []string{"disable", "remove"} {
			s, e := m.disk.read(dir+"/"+flag, false)
			if e != nil {
				return nil, nil, e
			}
			if s.Exists {
				t.Active = false
			}
			identity = append(identity, byte(0))
			if s.Exists {
				identity = append(identity, byte(1))
			}
		}
		switch id {
		case "tricky_store":
			t.ConfigDir = "/data/adb/tricky_store"
			rs, statusErr := m.teeSimulatorRS(&t, p)
			if statusErr != nil {
				return nil, nil, statusErr
			}
			switch {
			case rs:
				t.Backend = backend.TeeSimulatorRS
				t.Identity = "TEES-RS"
				t.ConfigPath = t.ConfigDir + "/target.txt"
			case t.VersionCode != nil && *t.VersionCode >= 246:
				t.Backend = backend.TrickyStore
				t.Identity = "TS"
				t.ConfigPath = t.ConfigDir + "/config.ini"
			default:
				t.Backend = backend.TrickyStoreLegacy
				t.Identity = "TS-L"
				t.ConfigPath = t.ConfigDir + "/target.txt"
			}
		case "oh_my_keymint":
			t.Backend = backend.OhMyKeymint
			t.Identity = "OMK"
			t.ConfigDir = "/data/misc/keystore/omk"
			t.ConfigPath = t.ConfigDir + "/config.toml"
		case "teesim":
			t.Backend = backend.TeeSimulator
			t.Identity = "TEES"
			t.ConfigDir = "/data/adb/teesim"
			t.ConfigPath = t.ConfigDir + "/config.json"
		}
		t.KeyboxPath = t.ConfigDir + "/keybox.xml"
		if selected == nil || (!selected.Active && t.Active) {
			copy := t
			selected = &copy
		}
	}
	return selected, identity, nil
}

// teeSimulatorRS mirrors backend.teeSimulatorRS: the fork keeps Tricky Store's
// module ID and target.txt layout, so only its module name and native certificate
// generator separate it from Tricky Store's config.ini versions.
func (m *manager) teeSimulatorRS(t *backend.Target, props map[string]string) (bool, error) {
	if strings.Contains(strings.ToLower(props["name"]), "teesimulator-rs") {
		return true, nil
	}
	s, native, err := m.disk.lstatEntry(t.ModuleDir + "/libcertgen.so")
	if err != nil {
		return false, err
	}
	return s.Exists && native, nil
}

// isSupported reports whether the active backend's app list is editable. Every
// detected backend keeps its list in a file this manager understands; backends
// with no app list of their own are not detected at all.
func isSupported(t *backend.Target) bool {
	if t == nil {
		return false
	}
	switch t.Backend {
	case backend.TrickyStore, backend.TrickyStoreLegacy, backend.TeeSimulatorRS, backend.TeeSimulator, backend.OhMyKeymint:
		return true
	}
	return false
}

type loaded struct {
	status Status
	state  state
	raw    snapshot
	doc    document
}

func (m *manager) load(packages bool) (loaded, error) {
	l := loaded{status: Status{Config: emptyConfig(), Packages: []Package{}, SystemApps: []string{}}}
	st, raw, err := m.loadState()
	if err != nil {
		return l, err
	}
	l.state = st
	l.raw = raw
	t, identity, err := m.detect()
	if err != nil {
		return l, err
	}
	l.status.Backend = t
	l.status.Supported = isSupported(t)
	l.status.SystemApps = st.SystemApps
	l.status.AutoAddNewApps = st.Auto
	if isSupported(t) {
		l.doc, err = readDocument(m.disk, t.Backend)
		if err != nil {
			l.status.ConfigError = err.Error()
		} else {
			l.status.Config = l.doc.config
		}
	}
	l.status.Entry, err = m.entryStatus(st, t)
	if err != nil {
		return l, err
	}
	// Revisions cover raw files, metadata, and persisted feature settings, not pm output.
	h := sha256.New()
	enc := json.NewEncoder(h)
	for _, v := range []any{identity, t, raw, st, l.doc.target, l.doc.patch, m.module} {
		if err = enc.Encode(v); err != nil {
			return l, err
		}
	}
	l.status.Revision = hex.EncodeToString(h.Sum(nil))
	if packages {
		installed, e := m.list()
		if e != nil {
			l.status.PackagesError = e.Error()
		}
		l.status.Packages = packageEntries(l.status.Config, st.SystemApps, installed)
	}
	return l, nil
}
func packageEntries(c Config, tracked []string, installed map[string]bool) []Package {
	all := map[string]Package{}
	for name, system := range installed {
		all[name] = Package{PackageName: name, System: system, Mode: "auto", Installed: true}
	}
	for _, p := range tracked {
		if _, ok := all[p]; !ok {
			all[p] = Package{PackageName: p, System: true, Mode: "auto"}
		}
	}
	for _, t := range c.Targets {
		p := all[t.PackageName]
		p.PackageName = t.PackageName
		p.Mode = t.Mode
		p.Selected = true
		all[t.PackageName] = p
	}
	out := make([]Package, 0, len(all))
	for _, p := range all {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PackageName < out[j].PackageName })
	return out
}
func (m *manager) status() (Status, error) {
	var s Status
	err := m.locked(func() error { l, e := m.load(true); s = l.status; return e })
	return s, err
}
func (m *manager) save(r SaveRequest) error {
	c := Config{append([]Target{}, r.Targets...), copyEditable(r.DefaultPolicy), map[string]Policy{}}
	for p, policy := range r.PerAppPolicy {
		c.PerAppPolicy[p] = copyEditable(policy)
	}
	if err := validateConfig(&c); err != nil {
		return err
	}
	if err := validateNames(r.SystemApps); err != nil {
		return err
	}
	return m.locked(func() error {
		if err := m.requireRunning(); err != nil {
			return err
		}
		l, err := m.load(false)
		if err != nil {
			return err
		}
		if !l.status.Supported {
			return fmt.Errorf("backend does not support targets")
		}
		if !l.status.Backend.Active {
			return fmt.Errorf("backend is disabled or pending removal")
		}
		if l.status.ConfigError != "" {
			return fmt.Errorf("config: %s", l.status.ConfigError)
		}
		if r.Revision == "" || r.Revision != l.status.Revision {
			return fmt.Errorf("revision conflict; refresh status and retry")
		}
		if l.doc.legacy && len(c.PerAppPolicy) > 0 {
			return fmt.Errorf("legacy Tricky Store does not support per-app policies")
		}
		if err := l.doc.validate(c); err != nil {
			return err
		}
		st := l.state
		st.SystemApps = append([]string{}, r.SystemApps...)
		st.Auto = r.AutoAddNewApps
		if st.Auto && !l.state.Auto {
			installed, e := m.list()
			if e != nil {
				return fmt.Errorf("cannot initialize auto baseline: %w", e)
			}
			st.Known = userNames(installed)
			st.Baseline = true
		} else if !st.Auto {
			st.Baseline = false
			st.Known = []string{}
		}
		sc, err := stateChange(st, l.raw)
		if err != nil {
			return err
		}
		return m.disk.transact(append(l.doc.changes(c), sc))
	})
}
func userNames(installed map[string]bool) []string {
	out := []string{}
	for p, system := range installed {
		if !system {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
func (m *manager) autoLocked() error {
	stop, e := m.stopped()
	if e != nil || stop {
		return e
	}
	l, err := m.load(false)
	if err != nil {
		return err
	}
	if !l.state.Auto {
		return nil
	}
	if !l.status.Supported {
		return fmt.Errorf("backend does not support auto targets")
	}
	if !l.status.Backend.Active {
		return fmt.Errorf("backend is disabled or pending removal")
	}
	if l.status.ConfigError != "" {
		return fmt.Errorf("config: %s", l.status.ConfigError)
	}
	installed, err := m.list()
	if err != nil {
		return err
	}
	st := l.state
	users := userNames(installed)
	changes := []change{}
	if st.Baseline {
		known := map[string]bool{}
		for _, p := range st.Known {
			known[p] = true
		}
		for _, t := range l.doc.config.Targets {
			known[t.PackageName] = true
		}
		c := l.doc.config
		for _, p := range users {
			if !known[p] {
				c.Targets = append(c.Targets, Target{p, "auto"})
			}
		}
		if len(c.Targets) != len(l.doc.config.Targets) {
			if err = validateConfig(&c); err != nil {
				return err
			}
			// A backend that owns one list per profile may reject the addition;
			// skipping keeps an optional boot-time auto-add from writing a bad file.
			if err = l.doc.validate(c); err != nil {
				return nil
			}
			changes = l.doc.changes(c)
		}
	}
	st.Known = users
	st.Baseline = true
	sc, err := stateChange(st, l.raw)
	if err != nil {
		return err
	}
	return m.disk.transact(append(changes, sc))
}
func (m *manager) auto() error { return m.locked(m.autoLocked) }

// limitedBuffer bounds both stdout and stderr, including malicious pm output.
type limitedBuffer struct {
	mu       sync.Mutex
	b        bytes.Buffer
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	left := maxIO - b.b.Len()
	if n > left {
		b.b.Write(p[:left])
		b.exceeded = true
		return n, fmt.Errorf("pm output exceeds IO limit")
	}
	b.b.Write(p)
	return n, nil
}
func listPackages(root string) (map[string]bool, error) {
	command, err := exec.LookPath("pm")
	if err != nil {
		return nil, fmt.Errorf("pm unavailable")
	}
	if root != "/" {
		absolute, e := filepath.Abs(command)
		if e != nil {
			return nil, e
		}
		resolved, e := filepath.EvalSymlinks(absolute)
		if e != nil {
			return nil, e
		}
		rel, e := filepath.Rel(root, resolved)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("isolated root requires a pm stub inside sysroot")
		}
	}
	out := map[string]bool{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, filter := range []string{"-3", "-s"} {
		cmd := exec.CommandContext(ctx, command, "list", "packages", filter)
		var stdout, stderr limitedBuffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		cmd.WaitDelay = 200 * time.Millisecond
		if err = cmd.Run(); err != nil || stdout.exceeded || stderr.exceeded {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("pm timed out")
			}
			return nil, fmt.Errorf("pm package listing failed")
		}
		count := 0
		for _, line := range strings.Split(stdout.b.String(), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			p, ok := strings.CutPrefix(line, "package:")
			if !ok || !validName(p) || isUID(p) {
				return nil, fmt.Errorf("invalid pm package output")
			}
			out[p] = filter == "-s"
			count++
		}
		if filter == "-s" && count == 0 {
			return nil, fmt.Errorf("packages unavailable: pm returned an empty system package list")
		}
	}
	return out, nil
}

// Boot restores entry links and adds newly observed user apps. module is the real
// absolute module directory on Android; tests use newManager with a private root.
func Boot(module string) error {
	m, err := newManager("/", module)
	if err != nil {
		return err
	}
	defer m.close()
	return m.boot()
}

// Cleanup removes only links pointing to this module, including disabled backends.
func Cleanup(module string) error {
	m, err := newManager("/", module)
	if err != nil {
		return err
	}
	defer m.close()
	return m.cleanup()
}
