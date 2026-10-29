package targets

import (
	"encoding/json"
	"strings"
	"testing"
)

func newBackendFixture(t *testing.T, id, prop, path, content string) (*manager, string) {
	t.Helper()
	root := t.TempDir()
	put(t, root, "/data/adb/modules/"+id+"/module.prop", prop)
	put(t, root, path, content)
	m, err := newManager(root, module)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.close)
	m.list = func() (map[string]bool, error) { return map[string]bool{}, nil }
	return m, root
}

func TestTeesimulatorAppListRoundTrip(t *testing.T) {
	m, root := newBackendFixture(t, "teesim", "id=teesim\nversionCode=1\n", "/data/adb/teesim/config.json",
		"{\"version\":1,\"profiles\":{\"default\":{\"keybox\":\"keys/a.xml\",\"mode\":\"patch\",\"apps\":[\"com.a\",\"com.b@10\"]},\"bank\":{\"apps\":[\"com.bank\"]}}}")
	s := getStatus(t, m)
	if !s.Supported || s.ConfigError != "" {
		t.Fatalf("%+v", s)
	}
	if len(s.Config.Targets) != 2 || s.Config.Targets[0].PackageName != "com.a" || s.Config.Targets[1].PackageName != "com.b@10" {
		t.Fatalf("%+v", s.Config.Targets)
	}
	r := request(s)
	r.Targets = []Target{{"com.c", "auto"}}
	if err := m.save(r); err != nil {
		t.Fatal(err)
	}
	var stored map[string]json.RawMessage
	if err := json.Unmarshal([]byte(read(t, root, "/data/adb/teesim/config.json")), &stored); err != nil {
		t.Fatal(err)
	}
	var profiles map[string]json.RawMessage
	json.Unmarshal(stored["profiles"], &profiles)
	var def, bank map[string]json.RawMessage
	json.Unmarshal(profiles["default"], &def)
	json.Unmarshal(profiles["bank"], &bank)
	if string(stored["version"]) != "1" || !strings.Contains(string(def["keybox"]), "keys/a.xml") || !strings.Contains(string(def["mode"]), "patch") {
		t.Fatalf("unrelated keys lost: %s %s %s", stored["version"], def["keybox"], def["mode"])
	}
	var apps, bankApps []string
	json.Unmarshal(def["apps"], &apps)
	json.Unmarshal(bank["apps"], &bankApps)
	if len(apps) != 1 || apps[0] != "com.c" || len(bankApps) != 1 || bankApps[0] != "com.bank" {
		t.Fatalf("apps=%v bank=%v", apps, bankApps)
	}
}

func TestTeesimulatorMissingDefaultProfileIsRefused(t *testing.T) {
	m, _ := newBackendFixture(t, "teesim", "id=teesim\nversionCode=1\n", "/data/adb/teesim/config.json",
		"{\"version\":1,\"profiles\":{\"bank\":{\"keybox\":\"b.xml\",\"apps\":[\"com.bank\"]}}}")
	if s := getStatus(t, m); s.ConfigError == "" {
		t.Fatal("config without a default profile accepted")
	}
}

func TestOhMyKeymintScoopRoundTrip(t *testing.T) {
	original := "# comment\n\nversion = 1\n\nscoop = [\n  \"com.a\",\n  \"com.b\", # trailing\n]\n\n[main]\nenabled = true\n\n[filter]\ndeny_packages = [\"com.x\"]\n"
	m, root := newBackendFixture(t, "oh_my_keymint", "id=oh_my_keymint\n", "/data/misc/keystore/omk/injector.toml", original)
	s := getStatus(t, m)
	if !s.Supported || s.ConfigError != "" {
		t.Fatalf("%+v", s)
	}
	if len(s.Config.Targets) != 2 || s.Config.Targets[0].PackageName != "com.a" || s.Config.Targets[1].PackageName != "com.b" {
		t.Fatalf("%+v", s.Config.Targets)
	}
	r := request(s)
	r.Targets = []Target{{"com.c", "auto"}, {"com.d", "auto"}}
	if err := m.save(r); err != nil {
		t.Fatal(err)
	}
	text := read(t, root, "/data/misc/keystore/omk/injector.toml")
	for _, want := range []string{"# comment", "version = 1", "[main]", "enabled = true", "[filter]", "deny_packages = [\"com.x\"]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	apps, _, _, err := ohMyKeymintScoop([]byte(text))
	if err != nil || len(apps) != 2 || apps[0] != "com.c" || apps[1] != "com.d" {
		t.Fatalf("%v %v\n%s", apps, err, text)
	}
}

func TestOhMyKeymintMissingScoopIsRefused(t *testing.T) {
	m, _ := newBackendFixture(t, "oh_my_keymint", "id=oh_my_keymint\n", "/data/misc/keystore/omk/injector.toml", "version = 1\n\n[main]\nenabled = true\n")
	if s := getStatus(t, m); s.ConfigError == "" {
		t.Fatal("injector.toml without scoop accepted")
	}
}

func TestTeesimulatorProfileOverlapIsRefused(t *testing.T) {
	m, root := newBackendFixture(t, "teesim", "id=teesim\nversionCode=1\n", "/data/adb/teesim/config.json",
		"{\"version\":1,\"profiles\":{\"default\":{\"keybox\":\"a.xml\",\"apps\":[\"com.a\"]},\"bank\":{\"keybox\":\"b.xml\",\"apps\":[\"com.bank\"]}}}")
	s := getStatus(t, m)
	if !s.Supported || s.ConfigError != "" {
		t.Fatalf("%+v", s)
	}
	r := request(s)
	r.Targets = append(r.Targets, Target{"com.bank", "auto"})
	if err := m.save(r); err == nil {
		t.Fatal("an app owned by another profile was accepted")
	}
	// A config that already overlaps is refused instead of being rewritten.
	put(t, root, "/data/adb/teesim/config.json",
		"{\"version\":1,\"profiles\":{\"default\":{\"keybox\":\"a.xml\",\"apps\":[\"com.a\"]},\"bank\":{\"keybox\":\"b.xml\",\"apps\":[\"com.a\"]}}}")
	if s := getStatus(t, m); s.ConfigError == "" {
		t.Fatal("pre-existing overlapping profiles accepted")
	}
}
