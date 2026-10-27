package backend

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func put(t *testing.T, root, devicePath, content string) string {
	t.Helper()
	p := hostPath(root, devicePath)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func module(t *testing.T, root, id, version string, flags ...string) {
	t.Helper()
	moduleNamed(t, root, id, "Test "+id, version, flags...)
}

func moduleNamed(t *testing.T, root, id, name, version string, flags ...string) {
	t.Helper()
	dir := "/data/adb/modules/" + id
	put(t, root, dir+"/module.prop", "# fixture\r\nid="+id+"\nname="+name+"\nversion=fixture\nversionCode="+version+"\n")
	for _, flag := range flags {
		put(t, root, dir+"/"+flag, "")
	}
}

func selected(t *testing.T, root string) *Target {
	t.Helper()
	report, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.Selected == nil {
		t.Fatal("no selected backend")
	}
	return report.Selected
}

func TestEveryBackendAndTrickyVersionBoundary(t *testing.T) {
	tests := []struct {
		module, version       string
		kind                  Kind
		identity, dir, config string
	}{
		{"tricky_store", "246", TrickyStore, "TS", "/data/adb/tricky_store", "config.ini"},
		{"tricky_store", "999", TrickyStore, "TS", "/data/adb/tricky_store", "config.ini"},
		{"tricky_store", "245", TrickyStoreLegacy, "TS-L", "/data/adb/tricky_store", "target.txt"},
		{"tricky_store", "", TrickyStoreLegacy, "TS-L", "/data/adb/tricky_store", "target.txt"},
		{"tricky_store", "invalid", TrickyStoreLegacy, "TS-L", "/data/adb/tricky_store", "target.txt"},
		{"tricky_store", "-1", TrickyStoreLegacy, "TS-L", "/data/adb/tricky_store", "target.txt"},
		{"tricky_store", "18446744073709551616", TrickyStoreLegacy, "TS-L", "/data/adb/tricky_store", "target.txt"},
		{"oh_my_keymint", "12", OhMyKeymint, "OMK", "/data/misc/keystore/omk", "config.toml"},
		{"teesim", "400", TeeSimulator, "TEES", "/data/adb/teesim", "config.json"},
	}
	for _, tc := range tests {
		t.Run(tc.module+"/"+tc.version, func(t *testing.T) {
			root := t.TempDir()
			module(t, root, tc.module, tc.version)
			got := selected(t, root)
			if got.Backend != tc.kind || got.Identity != tc.identity || got.ConfigDir != tc.dir ||
				got.ConfigPath != tc.dir+"/"+tc.config || got.KeyboxPath != tc.dir+"/keybox.xml" ||
				!got.Active || got.ModuleID != tc.module || got.ModuleDir != "/data/adb/modules/"+tc.module {
				t.Fatalf("unexpected detection: %+v", got)
			}
			if got.InstallMode != "atomic-replace" || got.NewFileMode != "0644" || got.Reload != "backend-managed" {
				t.Fatalf("unexpected install metadata: %+v", got)
			}
		})
	}
}

func TestTeeSimulatorRSDetection(t *testing.T) {
	// TEESimulator-RS reuses Tricky Store's module ID and target.txt layout with a
	// versionCode above Tricky Store's config.ini boundary.
	root := t.TempDir()
	moduleNamed(t, root, "tricky_store", "TEESimulator-RS", "307")
	got := selected(t, root)
	if got.Backend != TeeSimulatorRS || got.Identity != "TEES-RS" ||
		got.ConfigDir != "/data/adb/tricky_store" || got.ConfigPath != "/data/adb/tricky_store/target.txt" ||
		got.KeyboxPath != "/data/adb/tricky_store/keybox.xml" {
		t.Fatalf("unexpected RS detection: %+v", got)
	}
	// A renamed module is still recognised by its native certificate generator.
	root = t.TempDir()
	moduleNamed(t, root, "tricky_store", "Trick Store", "307")
	put(t, root, "/data/adb/modules/tricky_store/libcertgen.so", "native")
	if selected(t, root).Backend != TeeSimulatorRS {
		t.Fatal("native certificate generator marker ignored")
	}
	// Tricky Store keeps its own classification at every version.
	for _, tc := range []struct {
		version string
		want    Kind
		config  string
	}{
		{"307", TrickyStore, "config.ini"},
		{"246", TrickyStore, "config.ini"},
		{"245", TrickyStoreLegacy, "target.txt"},
		{"", TrickyStoreLegacy, "target.txt"},
	} {
		root = t.TempDir()
		moduleNamed(t, root, "tricky_store", "Tricky Store", tc.version)
		got = selected(t, root)
		if got.Backend != tc.want || got.ConfigPath != "/data/adb/tricky_store/"+tc.config {
			t.Fatalf("Tricky Store %q: %+v", tc.version, got)
		}
	}
}

func TestDetectionPrefersActiveAndReportsInstalled(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		tsFlags, omkFlags, teeFlags []string
		want                        Kind
		active                      bool
	}{
		{"all active", nil, nil, nil, TrickyStore, true},
		{"TS disabled", []string{"disable"}, nil, nil, OhMyKeymint, true},
		{"TS removing OMK disabled", []string{"remove"}, []string{"disable"}, nil, TeeSimulator, true},
		{"all inactive fall back first", []string{"disable"}, []string{"remove"}, []string{"disable"}, TrickyStore, false},
		{"update marker stays active", []string{"update"}, nil, nil, TrickyStore, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			module(t, root, "tricky_store", "246", tc.tsFlags...)
			module(t, root, "oh_my_keymint", "1", tc.omkFlags...)
			module(t, root, "teesim", "1", tc.teeFlags...)
			report, err := Detect(root)
			if err != nil || len(report.Backends) != 3 || report.Selected == nil ||
				report.Selected.Backend != tc.want || report.Selected.Active != tc.active {
				t.Fatalf("unexpected detection %+v, %v", report, err)
			}
		})
	}
}

func TestOnlyInstalledModulePropsCount(t *testing.T) {
	root := t.TempDir()
	put(t, root, "/data/adb/modules/tricky_store/anything", "")
	put(t, root, "/data/adb/modules_update/teesim/module.prop", "versionCode=999")
	report, err := Detect(root)
	if err != nil || report.Selected != nil || len(report.Backends) != 0 {
		t.Fatalf("uninstalled directories counted: %+v, %v", report, err)
	}
	module(t, root, "tricky_store", "245")
	put(t, root, "/data/adb/modules/tricky_store/module.prop", "versionCode=245\r\n versionCode = 246 \r\n")
	if selected(t, root).Backend != TrickyStore {
		t.Fatal("module.prop duplicate/CRLF semantics differ")
	}
}

func TestTeeDefaultProfilePaths(t *testing.T) {
	for _, tc := range []struct{ name, config, filename string }{
		{"missing config", "", "keybox.xml"},
		{"empty config", "{}", "keybox.xml"},
		{"other profile", `{"profiles":{"bank":{"keybox":"bank.xml"}}}`, "keybox.xml"},
		{"missing keybox", `{"profiles":{"default":{}}}`, "keybox.xml"},
		{"configured", `{"profiles":{"default":{"keybox":"custom.xml"}}}`, "custom.xml"},
		{"nested", `{"profiles":{"default":{"keybox":"keys/custom.xml"},"bank":{"keybox":"other.xml"}}}`, "keys/custom.xml"},
		{"space safe without shell eval", `{"profiles":{"default":{"keybox":"keys/custom key.xml"}}}`, "keys/custom key.xml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			module(t, root, "teesim", "1")
			if tc.config != "" {
				put(t, root, "/data/adb/teesim/config.json", tc.config)
			}
			if got := selected(t, root).KeyboxPath; got != "/data/adb/teesim/"+tc.filename {
				t.Fatalf("path = %q", got)
			}
		})
	}
}

func TestTeeRejectsUnsafePathsInsteadOfGuessing(t *testing.T) {
	for _, filename := range []string{"", "/data/local/tmp/key.xml", "../outside.xml", "keys/../../x", ".", "./x", "x/./y", "x//y", "x/", "x\\y", "x\ny", "x\ry", "x\ty", "x\x00y"} {
		t.Run(filename, func(t *testing.T) {
			root := t.TempDir()
			module(t, root, "teesim", "1")
			encoded, _ := json.Marshal(filename)
			put(t, root, "/data/adb/teesim/config.json", `{"profiles":{"default":{"keybox":`+string(encoded)+`}}}`)
			report, err := Detect(root)
			if err == nil || report.Selected == nil || report.Selected.KeyboxPath != "" || report.Selected.ConfigError == "" {
				t.Fatalf("unsafe path accepted: %+v, %v", report, err)
			}
		})
	}
	for _, config := range []string{"{", "null", "[]", `{"profiles":"bad"}`, `{"profiles":{"default":null}}`, `{"profiles":{"default":[]}}`, `{"profiles":{"default":{"keybox":123}}}`, `{"profiles":{"default":{"keybox":null}}}`, strings.Repeat(" ", 1024*1024+1)} {
		t.Run("invalid config", func(t *testing.T) {
			root := t.TempDir()
			module(t, root, "teesim", "1")
			put(t, root, "/data/adb/teesim/config.json", config)
			if _, err := Detect(root); err == nil {
				t.Fatalf("invalid config accepted: %.80q", config)
			}
		})
	}
}

func TestUnsafeInactiveBackendDoesNotBlockSelected(t *testing.T) {
	root := t.TempDir()
	module(t, root, "tricky_store", "246")
	module(t, root, "teesim", "1", "disable")
	put(t, root, "/data/adb/teesim/config.json", `{"profiles":{"default":{"keybox":"../bad"}}}`)
	report, err := Detect(root)
	if err != nil || report.Selected == nil || report.Selected.Backend != TrickyStore || report.Backends[1].ConfigError == "" {
		t.Fatalf("unselected config blocked good backend: %+v, %v", report, err)
	}
}

func TestRejectsSymlinkTargetsParentsAndConfigs(t *testing.T) {
	for _, linkPath := range []string{
		"/data/adb/teesim/config.json",
		"/data/adb/teesim/keybox.xml",
		"/data/adb/teesim/keys",
	} {
		t.Run(linkPath, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			module(t, root, "teesim", "1")
			keybox := "keybox.xml"
			if strings.HasSuffix(linkPath, "/keys") {
				keybox = "keys/custom.xml"
			}
			put(t, root, "/data/adb/teesim/config.json", `{"profiles":{"default":{"keybox":"`+keybox+`"}}}`)
			p := hostPath(root, linkPath)
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, p); err != nil {
				t.Fatal(err)
			}
			if _, err := Detect(root); err == nil {
				t.Fatal("symlink accepted")
			}
		})
	}
	root := t.TempDir()
	module(t, root, "tricky_store", "246")
	if err := os.MkdirAll(hostPath(root, "/data/adb/tricky_store/keybox.xml"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Detect(root); err == nil {
		t.Fatal("directory target accepted")
	}
}

func TestCLIJSONAndInstallOutputs(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	if rc := run([]string{"--root", root}, &stdout, &stderr); rc != 0 || !strings.Contains(stdout.String(), `"backends":[],"selected":null`) {
		t.Fatalf("no backend JSON: rc=%d out=%s err=%s", rc, &stdout, &stderr)
	}
	for _, mode := range []string{"target", "id"} {
		stdout.Reset()
		stderr.Reset()
		if rc := run([]string{mode, "--root", root}, &stdout, &stderr); rc != 1 || stdout.Len() != 0 {
			t.Fatalf("%s no backend output: %d, %s", mode, rc, &stdout)
		}
	}
	module(t, root, "teesim", "1")
	put(t, root, "/data/adb/teesim/config.json", `{"profiles":{"default":{"keybox":"nested/test.xml"}}}`)
	for mode, want := range map[string]string{"target": "/data/adb/teesim/nested/test.xml\n", "id": "tee-simulator\n"} {
		stdout.Reset()
		stderr.Reset()
		if rc := run([]string{"--root", root, mode}, &stdout, &stderr); rc != 0 || stdout.String() != want {
			t.Fatalf("%s rc=%d out=%q err=%s", mode, rc, stdout.String(), &stderr)
		}
	}
	put(t, root, "/data/adb/modules/teesim/disable", "")
	for _, mode := range []string{"target", "id"} {
		stdout.Reset()
		stderr.Reset()
		if rc := run([]string{mode, "--root", root}, &stdout, &stderr); rc != 1 || stdout.Len() != 0 {
			t.Fatalf("inactive %s emitted install output", mode)
		}
	}
	for _, args := range [][]string{{"unknown"}, {"--root"}, {"target", "extra"}, {"--root", ""}} {
		if rc := run(args, &stdout, &stderr); rc != 2 {
			t.Fatalf("bad args %q exit %d", args, rc)
		}
	}
}

func TestCLIInvalidConfigHasJSONButNoInstallTarget(t *testing.T) {
	root := t.TempDir()
	module(t, root, "teesim", "1")
	put(t, root, "/data/adb/teesim/config.json", "{")
	for _, mode := range []string{"json", "target", "id"} {
		var stdout, stderr bytes.Buffer
		rc := run([]string{mode, "--root", root}, &stdout, &stderr)
		if rc != 1 || stderr.Len() == 0 {
			t.Fatalf("%s invalid config exit=%d err=%s", mode, rc, &stderr)
		}
		if mode == "json" {
			var report Report
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Selected == nil || report.Selected.ConfigError == "" {
				t.Fatalf("invalid config report missing: %s", &stdout)
			}
		} else if stdout.Len() != 0 {
			t.Fatalf("invalid config %s leaked usable output", mode)
		}
	}
}
