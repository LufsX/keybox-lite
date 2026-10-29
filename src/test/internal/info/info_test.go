package info

import (
	"os"
	"path/filepath"
	"testing"

	"keyboxlite/internal/backend"
)

func fixtureFile(t *testing.T, root, devicePath, content string) {
	t.Helper()
	p := filepath.Join(root, devicePath)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestInfoUsesSharedActiveBackendAndDynamicPath(t *testing.T) {
	root := t.TempDir()
	fixtureFile(t, root, "/data/adb/modules/tricky_store/module.prop", "versionCode=246\n")
	fixtureFile(t, root, "/data/adb/modules/tricky_store/disable", "")
	fixtureFile(t, root, "/data/adb/modules/teesim/module.prop", "versionCode=1\n")
	fixtureFile(t, root, "/data/adb/teesim/config.json", `{"profiles":{"default":{"keybox":"nested/custom.xml"}}}`)
	fixtureFile(t, root, "/data/adb/teesim/nested/custom.xml", "<AndroidAttestation/>")
	result := collect(root, "")
	if result["path"] != "/data/adb/teesim/nested/custom.xml" || result["exists"] != true || result["structure_valid"] != true {
		t.Fatalf("info ignored selected backend: %+v", result)
	}
	target, ok := result["backend"].(*backend.Target)
	if !ok || target.Backend != backend.TeeSimulator || !target.Active {
		t.Fatalf("info backend metadata: %+v", result["backend"])
	}
	if len(result["backends"].([]backend.Target)) != 2 {
		t.Fatal("info omitted installed backends")
	}
}

func TestInfoDoesNotInventBackendOrFallbackFromUnsafeConfig(t *testing.T) {
	root := t.TempDir()
	fixtureFile(t, root, "/data/adb/tricky_store/keybox.xml", "<AndroidAttestation/>")
	result := collect(root, "")
	if result["path"] != "" || result["exists"] != false || result["backend"].(*backend.Target) != nil {
		t.Fatalf("guessed TS file with no backend: %+v", result)
	}
	fixtureFile(t, root, "/data/adb/modules/teesim/module.prop", "versionCode=1\n")
	fixtureFile(t, root, "/data/adb/teesim/config.json", `{"profiles":{"default":{"keybox":"../other.xml"}}}`)
	fixtureFile(t, root, "/data/adb/teesim/keybox.xml", "<AndroidAttestation/>")
	result = collect(root, "")
	if result["path"] != "" || result["exists"] != false || result["backend_error"] == nil {
		t.Fatalf("fell back from unsafe config: %+v", result)
	}
}

func TestInfoKeepsExplicitFileInspection(t *testing.T) {
	root := t.TempDir()
	fixtureFile(t, root, "/custom/inspection.xml", "<AndroidAttestation/>")
	result := collect(root, "/custom/inspection.xml")
	if result["path"] != "/custom/inspection.xml" || result["exists"] != true || result["structure_valid"] != true {
		t.Fatalf("explicit inspection failed: %+v", result)
	}
	result = collect(root, "../outside.xml")
	if result["exists"] != false || result["error"] == nil {
		t.Fatalf("sysroot override traversed: %+v", result)
	}
}
