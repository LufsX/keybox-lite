package targets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const module = "/data/adb/modules/keybox-lite"
const configPath = "/data/adb/tricky_store/config.ini"
const targetPath = "/data/adb/tricky_store/target.txt"
const patchPath = "/data/adb/tricky_store/security_patch.txt"

func put(t *testing.T, root, p, text string) {
	t.Helper()
	p = filepath.Join(root, p)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T, legacy bool) (*manager, string) {
	t.Helper()
	root := t.TempDir()
	version := "246"
	if legacy {
		version = "245"
	}
	put(t, root, "/data/adb/modules/tricky_store/module.prop", "id=tricky_store\nversionCode="+version+"\n")
	put(t, root, module+"/webroot/index.html", "test")
	put(t, root, module+"/webui.sh", "#!/system/bin/sh\n")
	m, err := newManager(root, module)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.close)
	m.list = func() (map[string]bool, error) { return map[string]bool{"com.user": false, "com.system": true}, nil }
	return m, root
}
func getStatus(t *testing.T, m *manager) Status {
	t.Helper()
	s, err := m.status()
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func request(s Status) SaveRequest {
	return SaveRequest{Revision: s.Revision, Targets: s.Config.Targets, DefaultPolicy: s.Config.DefaultPolicy, PerAppPolicy: s.Config.PerAppPolicy, SystemApps: s.SystemApps, AutoAddNewApps: s.AutoAddNewApps}
}
func read(t *testing.T, root, p string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, p))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func absent(t *testing.T, root, p string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, p)); !os.IsNotExist(err) {
		t.Fatalf("%s exists: %v", p, err)
	}
}
func TestModernRoundTripPreservesUnknownAndEditablePolicies(t *testing.T) {
	m, root := fixture(t, false)
	raw := "# header\n[android]\nos_patch = 202601\ncustom = android-value\n[target]\ncom.a\ncom.b!\nuid:10042?\ncom.user@10!\nandroid\n[default_policy]\nos_patch = prop\nvendor_patch = 20260228\nunknown = retain\n[com.b]\nboot_patch = no\nfuture = yes\n[future_feature]\nos_patch = future-syntax\nflag = unchanged\n"
	put(t, root, configPath, raw)
	s := getStatus(t, m)
	if s.ConfigError != "" {
		t.Fatal(s.ConfigError)
	}
	if len(s.Config.Targets) != 5 || s.Config.PerAppPolicy["android"]["os_patch"] != "202601" {
		t.Fatalf("%+v", s.Config)
	}
	if len(s.Config.DefaultPolicy) != 2 || len(s.Config.PerAppPolicy["com.b"]) != 1 {
		t.Fatalf("unknown keys exposed: %+v", s.Config)
	}
	r := request(s)
	r.DefaultPolicy = Policy{"os_patch": "202610", "boot_patch": ""}
	r.PerAppPolicy = map[string]Policy{"com.b": {"boot_patch": "20261001"}, "android": {"os_patch": "prop"}}
	if err := m.save(r); err != nil {
		t.Fatal(err)
	}
	written := read(t, root, configPath)
	for _, want := range []string{"# header", "unknown = retain", "future = yes", "custom = android-value", "[future_feature]\nos_patch = future-syntax\nflag = unchanged", "com.b!", "uid:10042?", "com.user@10!", "boot_patch = 20261001"} {
		if !strings.Contains(written, want) {
			t.Fatalf("missing %q in %s", want, written)
		}
	}
	if strings.Contains(written, "vendor_patch =") {
		t.Fatal("omitted known field was not cleared")
	}
	next := getStatus(t, m)
	if next.ConfigError != "" || next.Config.PerAppPolicy["com.b"]["boot_patch"] != "20261001" {
		t.Fatalf("%+v", next)
	}
	// A manual package remains visible even when not installed.
	for _, p := range next.Packages {
		if p.PackageName == "uid:10042" && (!p.Selected || p.Installed || p.Mode != "hack") {
			t.Fatalf("%+v", p)
		}
	}
}
func TestLegacyRoundTripAndBarePatch(t *testing.T) {
	m, root := fixture(t, true)
	put(t, root, targetPath, "# target comments\ncom.a!\ncom.b?\nuid:123\n")
	put(t, root, patchPath, "# patch comments\n20241101\nfuture_key=keep\n")
	s := getStatus(t, m)
	if s.ConfigError != "" {
		t.Fatal(s.ConfigError)
	}
	if s.Config.DefaultPolicy["os_patch"] != "202411" || s.Config.DefaultPolicy["boot_patch"] != "20241101" {
		t.Fatalf("%+v", s.Config)
	}
	r := request(s)
	r.DefaultPolicy = Policy{"os_patch": "no", "vendor_patch": "prop", "boot_patch": "20260228"}
	if err := m.save(r); err != nil {
		t.Fatal(err)
	}
	if text := read(t, root, patchPath); !strings.Contains(text, "future_key=keep") || !strings.Contains(text, "system=no") || !strings.Contains(text, "vendor=prop") {
		t.Fatal(text)
	}
	if text := read(t, root, targetPath); !strings.Contains(text, "com.a!") || !strings.Contains(text, "com.b?") || !strings.Contains(text, "# target comments") {
		t.Fatal(text)
	}
	s = getStatus(t, m)
	r = request(s)
	r.PerAppPolicy = map[string]Policy{"com.a": {"os_patch": "prop"}}
	if err := m.save(r); err == nil {
		t.Fatal("legacy per-app policy accepted")
	}
}
func TestInputAndCorruptionRejectedWithoutWrites(t *testing.T) {
	for _, name := range []string{"com[a", "com/a", "com..a", "com.a\n[target]", "com.a;id", "$(id)", "uid:+1", "uid:4294967296", "uid:", "com.a@-1", "com.a@2@3", "com.a!", "[target]", "com.a\\evil"} {
		if validName(name) {
			t.Errorf("accepted name %q", name)
		}
	}
	for _, name := range []string{"android", "com.a", "uid:10042", "com.a@10", "_test.a1"} {
		if !validName(name) {
			t.Errorf("rejected name %q", name)
		}
	}
	for _, v := range []string{"000001", "189912", "202613", "202600", "2026-01", "202601\nx=1"} {
		if validPatch("os_patch", v) {
			t.Errorf("accepted patch %q", v)
		}
	}
	if validPatch("boot_patch", "20260230") || !validPatch("boot_patch", "20240229") {
		t.Fatal("calendar validation")
	}
	for _, raw := range []string{"[target\ncom.a\n", "[target]\ncom.a\n[target]\ncom.b\n", "[target]\ncom.a;\n", "[default_policy]\nos_patch=202613\n", "[com.a]\nboot_patch=20260230\n", "[target]\ncom.a\ncom.a?\n", "[target]\ncom.a\n\x00"} {
		t.Run(fmt.Sprintf("%x", []byte(raw)[:8]), func(t *testing.T) {
			m, root := fixture(t, false)
			put(t, root, configPath, raw)
			s := getStatus(t, m)
			if s.ConfigError == "" {
				t.Fatal("missing config_error")
			}
			r := request(s)
			r.Targets = []Target{{"com.safe", "auto"}}
			if err := m.save(r); err == nil {
				t.Fatal("corrupt config overwritten")
			}
			if read(t, root, configPath) != raw {
				t.Fatal("corrupt config changed")
			}
			absent(t, root, statePath)
		})
	}
	m, root := fixture(t, false)
	put(t, root, configPath, "[target]\ncom.a\n")
	s := getStatus(t, m)
	for _, r := range []SaveRequest{
		{Revision: s.Revision, Targets: []Target{{"com.a;id", "auto"}}},
		{Revision: s.Revision, Targets: []Target{{"com.a", "evil"}}},
		{Revision: s.Revision, DefaultPolicy: Policy{"unknown": "value"}},
		{Revision: s.Revision, SystemApps: []string{"com.a\nrm"}},
	} {
		if err := m.save(r); err == nil {
			t.Fatal("invalid request accepted")
		}
		if read(t, root, configPath) != "[target]\ncom.a\n" {
			t.Fatal("invalid request wrote file")
		}
	}
}
func TestRevisionConflictAndConcurrentWriters(t *testing.T) {
	m, root := fixture(t, false)
	put(t, root, configPath, "[target]\ncom.a\n")
	s := getStatus(t, m)
	r := request(s)
	r.Targets = []Target{{"com.b", "hack"}}
	m2, err := newManager(root, module)
	if err != nil {
		t.Fatal(err)
	}
	defer m2.close()
	m2.list = m.list
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, mm := range []*manager{m, m2} {
		wg.Add(1)
		go func(mm *manager) { defer wg.Done(); <-start; errs <- mm.save(r) }(mm)
	}
	close(start)
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if strings.Contains(err.Error(), "revision conflict") {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success %d conflict %d", success, conflict)
	}
	before := read(t, root, configPath)
	if err = m.save(r); err == nil {
		t.Fatal("stale revision accepted")
	}
	if read(t, root, configPath) != before {
		t.Fatal("stale request changed config")
	}
	s = getStatus(t, m)
	r = request(s)
	put(t, root, configPath, before+"# external edit\n")
	if err = m.save(r); err == nil {
		t.Fatal("external edit was not detected")
	}
}
func TestRollbackSecondCommitAndStateFailure(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, failState := range []bool{false, true} {
			t.Run(fmt.Sprintf("legacy%v-state%v", legacy, failState), func(t *testing.T) {
				m, root := fixture(t, legacy)
				p := configPath
				if legacy {
					p = targetPath
					put(t, root, patchPath, "all=20250101\nfuture=keep\n")
				}
				put(t, root, p, "[target]\ncom.a\n")
				if legacy {
					put(t, root, p, "com.a\n")
				}
				// Initialize state so rollback also verifies an existing private state file.
				s := getStatus(t, m)
				if err := m.save(request(s)); err != nil {
					t.Fatal(err)
				}
				s = getStatus(t, m)
				oldConfig := read(t, root, p)
				oldState := read(t, root, statePath)
				oldPatch := ""
				if legacy {
					oldPatch = read(t, root, patchPath)
				}
				if err := os.Chmod(filepath.Join(root, p), 0640); err != nil {
					t.Fatal(err)
				}
				s = getStatus(t, m)
				r := request(s)
				r.Targets = []Target{{"com.changed", "generate"}}
				r.DefaultPolicy = Policy{"os_patch": "202610"}
				r.AutoAddNewApps = true
				failPath := statePath
				if legacy && !failState {
					failPath = patchPath
				}
				injected := false
				observedPartial := false
				m.disk.beforeCommit = func(path string, want snapshot) error {
					if path == failPath && !injected {
						injected = true
						observedPartial = read(t, root, p) != oldConfig
						return errors.New("injected rename failure")
					}
					return nil
				}
				if err := m.save(r); err == nil || !strings.Contains(err.Error(), "injected") {
					t.Fatalf("save: %v", err)
				}
				if !injected || !observedPartial {
					t.Fatal("failure was not injected after config commit")
				}
				if read(t, root, p) != oldConfig || read(t, root, statePath) != oldState {
					t.Fatal("config/state not rolled back")
				}
				if legacy && read(t, root, patchPath) != oldPatch {
					t.Fatal("patch not rolled back")
				}
				info, _ := os.Stat(filepath.Join(root, p))
				if info.Mode().Perm() != 0640 {
					t.Fatal("permissions lost")
				}
				absent(t, root, stateDir+"/transaction.json")
				m.disk.beforeCommit = nil
				r = request(getStatus(t, m))
				r.Targets = []Target{{"com.retry", "hack"}}
				if err := m.save(r); err != nil {
					t.Fatalf("retry failed: %v", err)
				}
			})
		}
	}
}
func TestInterruptedTransactionRecoveredAndForeignChangesProtected(t *testing.T) {
	m, root := fixture(t, false)
	put(t, root, configPath, "[target]\ncom.old\n")
	old, err := m.disk.read(configPath, false)
	if err != nil {
		t.Fatal(err)
	}
	next := fileImage([]byte("[target]\ncom.next!\n"), old, false)
	j, _ := json.Marshal(journal{Version: 1, Changes: []change{{configPath, old, next}}})
	if err = m.disk.write(stateDir+"/transaction.json", fileImage(j, snapshot{}, true)); err != nil {
		t.Fatal(err)
	}
	if err = m.disk.write(configPath, next); err != nil {
		t.Fatal(err)
	}
	if getStatus(t, m).Config.Targets[0].PackageName != "com.old" {
		t.Fatal("interrupted write not recovered")
	}
	absent(t, root, stateDir+"/transaction.json")
	// A changed foreign file must block recovery, never be overwritten.
	if err = m.disk.write(stateDir+"/transaction.json", fileImage(j, snapshot{}, true)); err != nil {
		t.Fatal(err)
	}
	put(t, root, configPath, "[target]\ncom.foreign\n")
	if _, err = m.status(); err == nil || !strings.Contains(err.Error(), "recovery conflict") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(read(t, root, configPath), "com.foreign") {
		t.Fatal("foreign config overwritten")
	}
}
func TestSymlinkIsolationAndPermissions(t *testing.T) {
	for _, kind := range []string{"config", "parent", "state", "lock", "root"} {
		t.Run(kind, func(t *testing.T) {
			m, root := fixture(t, false)
			outside := t.TempDir()
			put(t, outside, "/config.ini", "outside")
			p := configPath
			switch kind {
			case "config":
				os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0755)
				if err := os.Symlink(filepath.Join(outside, "config.ini"), filepath.Join(root, p)); err != nil {
					t.Fatal(err)
				}
			case "parent":
				if err := os.Symlink(outside, filepath.Join(root, "data/adb/tricky_store")); err != nil {
					t.Fatal(err)
				}
			case "state":
				os.MkdirAll(filepath.Join(root, filepath.Dir(stateDir)), 0755)
				if err := os.Symlink(outside, filepath.Join(root, stateDir)); err != nil {
					t.Fatal(err)
				}
			case "lock":
				os.MkdirAll(filepath.Join(root, stateDir), 0700)
				if err := os.Symlink(filepath.Join(outside, "config.ini"), filepath.Join(root, stateDir, "lock")); err != nil {
					t.Fatal(err)
				}
			case "root":
				linked := filepath.Join(t.TempDir(), "linked")
				if err := os.Symlink(root, linked); err != nil {
					t.Fatal(err)
				}
				mm, err := newManager(linked, module)
				if err == nil {
					mm.close()
					t.Fatal("symlink root accepted")
				}
				return
			}
			s, err := m.status()
			if err == nil && s.ConfigError == "" {
				t.Fatal("symlink not rejected")
			}
			if err == nil {
				r := request(s)
				if err = m.save(r); err == nil {
					t.Fatal("symlink save accepted")
				}
			}
			if read(t, outside, "/config.ini") != "outside" {
				t.Fatal("escaped sysroot")
			}
		})
	}
	m, root := fixture(t, false)
	s := getStatus(t, m)
	r := request(s)
	r.Targets = []Target{{"android", "auto"}}
	if err := m.save(r); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{configPath: 0644, statePath: 0600, stateDir: 0700, "/data": 0755, "/data/adb": 0755, "/data/adb/tricky_store": 0755} {
		st, err := os.Stat(filepath.Join(root, p))
		if err != nil || st.Mode().Perm() != want {
			t.Fatalf("%s mode=%v err=%v", p, st, err)
		}
	}
}
func TestAutoBaselineNewUserAndPMFailure(t *testing.T) {
	m, root := fixture(t, false)
	users := map[string]bool{"com.old": false, "com.system": true}
	pmErr := error(nil)
	m.list = func() (map[string]bool, error) { return users, pmErr }
	s := getStatus(t, m)
	r := request(s)
	r.AutoAddNewApps = true
	pmErr = errors.New("pm offline")
	if err := m.save(r); err == nil {
		t.Fatal("baseline failure accepted")
	}
	absent(t, root, configPath)
	absent(t, root, statePath)
	pmErr = nil
	if err := m.save(r); err != nil {
		t.Fatal(err)
	}
	if len(getStatus(t, m).Config.Targets) != 0 {
		t.Fatal("baseline mass-added apps")
	}
	users["com.new"] = false
	users["com.newsystem"] = true
	if err := m.auto(); err != nil {
		t.Fatal(err)
	}
	s = getStatus(t, m)
	if len(s.Config.Targets) != 1 || s.Config.Targets[0].PackageName != "com.new" {
		t.Fatalf("%+v", s.Config)
	}
	before := read(t, root, statePath)
	pmErr = errors.New("pm offline")
	if err := m.auto(); err == nil {
		t.Fatal("pm failure accepted")
	}
	if read(t, root, statePath) != before {
		t.Fatal("pm failure changed baseline")
	}
	s = getStatus(t, m)
	if s.PackagesError == "" {
		t.Fatal("pm failure not reported")
	}
	// Old apps removed and installed again are new relative to the last successful boot.
	pmErr = nil
	delete(users, "com.old")
	if err := m.auto(); err != nil {
		t.Fatal(err)
	}
	users["com.old"] = false
	if err := m.auto(); err != nil {
		t.Fatal(err)
	}
	if len(getStatus(t, m).Config.Targets) != 2 {
		t.Fatal("reinstalled user app not added")
	}
	// Uninitialized enabled state records a baseline on first successful auto.
	st, raw, err := m.loadState()
	if err != nil {
		t.Fatal(err)
	}
	st.Baseline = false
	st.Known = []string{}
	sc, _ := stateChange(st, raw)
	if err = m.disk.transact([]change{sc}); err != nil {
		t.Fatal(err)
	}
	users["com.baseline"] = false
	if err = m.auto(); err != nil {
		t.Fatal(err)
	}
	for _, tt := range getStatus(t, m).Config.Targets {
		if tt.PackageName == "com.baseline" {
			t.Fatal("first boot mass-added")
		}
	}
}
func TestCLIIsolationAcknowledgmentsAndLimits(t *testing.T) {
	m, root := fixture(t, false)
	_ = m
	// No executable outside root is consulted for package enumeration.
	t.Setenv("PATH", "/nonexistent")
	var out, stderr bytes.Buffer
	args := []string{"--root", root, "--module-dir", module}
	if code := run(append(args, "status"), strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatalf("%d %s", code, stderr.String())
	}
	var s Status
	if err := json.Unmarshal(out.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.PackagesError == "" || !s.Supported {
		t.Fatalf("%+v", s)
	}
	r := request(s)
	r.Targets = []Target{{"com.saved", "generate"}}
	data, _ := json.Marshal(r)
	out.Reset()
	stderr.Reset()
	if code := run(append(args, "save"), bytes.NewReader(data), &out, &stderr); code != 0 || strings.TrimSpace(out.String()) != "{\"ok\":true}" {
		t.Fatalf("%d %s %s", code, out.String(), stderr.String())
	}
	out.Reset()
	if code := run(append(args, "entry", "set", "on"), strings.NewReader(""), &out, &stderr); code != 0 || strings.TrimSpace(out.String()) != "{\"ok\":true}" {
		t.Fatalf("%d %s", code, stderr.String())
	}
	for _, input := range []string{"null", "{} {}", strings.Repeat(" ", maxIO+1), "{\"revision\":\"x\",\"unknown\":1}"} {
		out.Reset()
		if code := run(append(args, "save"), strings.NewReader(input), &out, &stderr); code == 0 {
			t.Fatal("invalid JSON accepted")
		}
	}
	if code := run([]string{"--root"}, strings.NewReader(""), &out, &stderr); code != 2 {
		t.Fatal("bad arguments exit")
	}
}
func TestPMExecOutputFailureAndTimeout(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	os.MkdirAll(bin, 0755)
	script := filepath.Join(bin, "pm")
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	writeScript := func(s string) {
		t.Helper()
		if err := os.WriteFile(script, []byte("#!/bin/sh\n"+s), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeScript("case \"$*\" in *-s*) printf 'package:android\\n';; *) printf 'package:com.user\\npackage:com.user@10\\n';; esac\n")
	list, err := listPackages(root)
	if err != nil || len(list) != 3 || !list["android"] {
		t.Fatalf("%v %v", list, err)
	}
	writeScript("printf 'package:com.bad;id\\n'\n")
	if _, err = listPackages(root); err == nil {
		t.Fatal("pm injection accepted")
	}
	writeScript("printf 'package:com.partial\\n'; exit 1\n")
	if _, err = listPackages(root); err == nil {
		t.Fatal("partial pm success accepted")
	}
	writeScript("head -c 1048580 /dev/zero\n")
	if _, err = listPackages(root); err == nil {
		t.Fatal("pm output limit not enforced")
	}
	writeScript("exec sleep 20\n")
	start := time.Now()
	if _, err = listPackages(root); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("%v", err)
	}
	if time.Since(start) > 7*time.Second {
		t.Fatal("pm timeout exceeded")
	}
}
