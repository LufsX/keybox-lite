package targets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const entryPath = "/data/adb/modules/tricky_store/webroot"
const actionPath = "/data/adb/modules/tricky_store/action.sh"

func symlink(t *testing.T, root, p, to string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(to, filepath.Join(root, p)); err != nil {
		t.Fatal(err)
	}
}
func requireLink(t *testing.T, root, p, to string) {
	t.Helper()
	got, err := os.Readlink(filepath.Join(root, p))
	if err != nil || got != to {
		t.Fatalf("%s -> %q want %q err %v", p, got, to, err)
	}
}
func TestEntrySetBootRestoreOffAndCleanup(t *testing.T) {
	m, root := fixture(t, false)
	if err := m.entrySet(true); err != nil {
		t.Fatal(err)
	}
	s := getStatus(t, m)
	if !s.Entry.Enabled || !s.Entry.Linked || s.Entry.HasOwnWebUI || s.Entry.ModuleID == nil || *s.Entry.ModuleID != "tricky_store" {
		t.Fatalf("%+v", s.Entry)
	}
	requireLink(t, root, entryPath, module+"/webroot")
	if err := os.Remove(filepath.Join(root, entryPath)); err != nil {
		t.Fatal(err)
	}
	if err := m.boot(); err != nil {
		t.Fatal(err)
	}
	requireLink(t, root, entryPath, module+"/webroot")
	if err := m.entrySet(false); err != nil {
		t.Fatal(err)
	}
	absent(t, root, entryPath)
	if err := m.boot(); err != nil {
		t.Fatal(err)
	}
	absent(t, root, entryPath)
	if err := m.entrySet(true); err != nil {
		t.Fatal(err)
	}
	// Cleanup scans known backend paths even without module.prop, including disabled
	// and pending-removal modules left by previous active-backend selections.
	for _, id := range []string{"oh_my_keymint", "teesim"} {
		symlink(t, root, "/data/adb/modules/"+id+"/webroot", module+"/webroot")
		symlink(t, root, "/data/adb/modules/"+id+"/action.sh", module+"/webui.sh")
		put(t, root, "/data/adb/modules/"+id+"/disable", "")
	}
	if err := m.cleanup(); err != nil {
		t.Fatal(err)
	}
	for _, id := range backendIDs {
		absent(t, root, "/data/adb/modules/"+id+"/webroot")
		absent(t, root, "/data/adb/modules/"+id+"/action.sh")
	}
	if getStatus(t, m).Entry.Enabled {
		t.Fatal("cleanup did not disable state")
	}
}
func TestEntryNativeForeignAndDanglingWebUIAreNeverOverwritten(t *testing.T) {
	for _, kind := range []string{"directory", "file", "foreign", "dangling", "relative"} {
		t.Run(kind, func(t *testing.T) {
			m, root := fixture(t, false)
			switch kind {
			case "directory":
				os.MkdirAll(filepath.Join(root, entryPath), 0755)
			case "file":
				put(t, root, entryPath, "native")
			case "foreign":
				os.MkdirAll(filepath.Join(root, "foreign"), 0755)
				symlink(t, root, entryPath, filepath.Join(root, "foreign"))
			case "dangling":
				symlink(t, root, entryPath, "/missing/foreign")
			case "relative":
				symlink(t, root, entryPath, "../keybox-lite/webroot")
			}
			before, _, err := m.disk.lstatEntry(entryPath)
			if err != nil {
				t.Fatal(err)
			}
			if err = m.entrySet(true); err == nil {
				t.Fatal("existing WebUI overwritten")
			}
			s := getStatus(t, m)
			if s.Entry.Enabled || s.Entry.Linked || !s.Entry.HasOwnWebUI {
				t.Fatalf("%+v", s.Entry)
			}
			if err = m.entrySet(false); err != nil {
				t.Fatal(err)
			}
			if err = m.cleanup(); err != nil {
				t.Fatal(err)
			}
			after, _, err := m.disk.lstatEntry(entryPath)
			if err != nil || !same(before, after) {
				t.Fatal("foreign/native entry changed")
			}
		})
	}
}
func TestMagiskActionUsesLauncherAndKeepsBackendActions(t *testing.T) {
	for _, existing := range []string{"none", "native", "foreign", "dangling"} {
		t.Run(existing, func(t *testing.T) {
			m, root := fixture(t, false)
			os.MkdirAll(filepath.Join(root, "data/adb/magisk"), 0755)
			switch existing {
			case "native":
				put(t, root, actionPath, "native action\n")
			case "foreign":
				symlink(t, root, actionPath, module+"/action.sh")
			case "dangling":
				symlink(t, root, actionPath, "/foreign/missing")
			}
			before, native, err := m.disk.lstatEntry(actionPath)
			if err != nil {
				t.Fatal(err)
			}
			if err = m.entrySet(true); err != nil {
				t.Fatal(err)
			}
			requireLink(t, root, entryPath, module+"/webroot")
			if existing == "none" {
				requireLink(t, root, actionPath, module+"/webui.sh")
			} else {
				after, native2, err := m.disk.lstatEntry(actionPath)
				if err != nil || native != native2 || !same(before, after) {
					t.Fatal("existing action overwritten")
				}
			}
			if err = m.entrySet(false); err != nil {
				t.Fatal(err)
			}
			absent(t, root, entryPath)
			if existing == "none" {
				absent(t, root, actionPath)
			} else {
				after, _, err := m.disk.lstatEntry(actionPath)
				if err != nil || !same(before, after) {
					t.Fatal("existing action removed")
				}
			}
		})
	}
}
func TestEntryMisconfiguredBackendAndActiveSwitch(t *testing.T) {
	m, root := fixture(t, false)
	put(t, root, "/data/adb/modules/tricky_store/disable", "")
	put(t, root, "/data/adb/modules/oh_my_keymint/module.prop", "id=oh_my_keymint\n")
	s := getStatus(t, m)
	if !s.Supported || s.Backend == nil || s.Backend.ModuleID != "oh_my_keymint" || s.ConfigError == "" {
		t.Fatalf("%+v", s)
	}
	if err := m.save(request(s)); err == nil {
		t.Fatal("targets accepted without a readable injector.toml")
	}
	if err := m.entrySet(true); err != nil {
		t.Fatal(err)
	}
	omkEntry := "/data/adb/modules/oh_my_keymint/webroot"
	requireLink(t, root, omkEntry, module+"/webroot")
	os.Remove(filepath.Join(root, "/data/adb/modules/tricky_store/disable"))
	if err := m.boot(); err != nil {
		t.Fatal(err)
	}
	requireLink(t, root, entryPath, module+"/webroot")
	absent(t, root, omkEntry)
	// Off removes every owned link even when active selection changes again.
	symlink(t, root, omkEntry, module+"/webroot")
	if err := m.entrySet(false); err != nil {
		t.Fatal(err)
	}
	absent(t, root, entryPath)
	absent(t, root, omkEntry)
}
func TestTeeSimulatorRSAppListUsesTargetTxt(t *testing.T) {
	root := t.TempDir()
	put(t, root, "/data/adb/modules/tricky_store/module.prop", "id=tricky_store\nname=TEESimulator-RS\nversionCode=307\n")
	put(t, root, targetPath, "com.a!\n")
	put(t, root, patchPath, "system=202411\n")
	m, err := newManager(root, module)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.close)
	m.list = func() (map[string]bool, error) { return map[string]bool{}, nil }
	s := getStatus(t, m)
	if s.Backend == nil || s.Backend.Backend != "tee-simulator-rs" || s.Backend.ConfigPath != targetPath ||
		s.Backend.KeyboxPath != "/data/adb/tricky_store/keybox.xml" {
		t.Fatalf("%+v", s.Backend)
	}
	if !s.Supported || s.ConfigError != "" || len(s.Config.Targets) != 1 ||
		s.Config.Targets[0].PackageName != "com.a" || s.Config.Targets[0].Mode != "generate" {
		t.Fatalf("RS target list not manageable: %+v", s)
	}
	r := request(s)
	r.Targets = append(r.Targets, Target{"com.b", "auto"})
	if err := m.save(r); err != nil {
		t.Fatal(err)
	}
	if text := read(t, root, targetPath); !strings.Contains(text, "com.a!") || !strings.Contains(text, "com.b") {
		t.Fatal(text)
	}
	// A keybox-scoped list is refused instead of being rewritten without its scope.
	put(t, root, targetPath, "[custom.xml]\ncom.a!\n")
	if s := getStatus(t, m); s.ConfigError == "" {
		t.Fatal("keybox-scoped RS target.txt accepted")
	}
}

func TestEntryTransactionRollbackAndRetry(t *testing.T) {
	m, root := fixture(t, false)
	os.MkdirAll(filepath.Join(root, "data/adb/magisk"), 0755)
	injected := false
	m.disk.beforeCommit = func(p string, w snapshot) error {
		if p == statePath && !injected {
			injected = true
			requireLink(t, root, entryPath, module+"/webroot")
			requireLink(t, root, actionPath, module+"/webui.sh")
			return errors.New("state commit fails")
		}
		return nil
	}
	if err := m.entrySet(true); err == nil {
		t.Fatal("injected error lost")
	}
	absent(t, root, entryPath)
	absent(t, root, actionPath)
	absent(t, root, statePath)
	m.disk.beforeCommit = nil
	if err := m.entrySet(true); err != nil {
		t.Fatal(err)
	}
	// A module update may restore a native WebUI. Boot must report that conflict.
	os.Remove(filepath.Join(root, entryPath))
	os.MkdirAll(filepath.Join(root, entryPath), 0755)
	if err := m.boot(); err == nil {
		t.Fatal("native update conflict not reported")
	}
	if st, err := os.Stat(filepath.Join(root, entryPath)); err != nil || !st.IsDir() {
		t.Fatal("native WebUI overwritten on boot")
	}
}
func TestCleanupWithDamagedStateAndForeignModuleMetadata(t *testing.T) {
	m, root := fixture(t, false)
	symlink(t, root, entryPath, module+"/webroot")
	symlink(t, root, actionPath, module+"/webui.sh")
	put(t, root, statePath, "{broken")
	os.Remove(filepath.Join(root, "/data/adb/modules/tricky_store/module.prop"))
	symlink(t, root, "/data/adb/modules/tricky_store/module.prop", "/missing/foreign")
	if err := m.cleanup(); err != nil {
		t.Fatal(err)
	}
	absent(t, root, entryPath)
	absent(t, root, actionPath)
	if read(t, root, statePath) != "{broken" {
		t.Fatal("damaged state overwritten")
	}
}
func TestStoppingCheckedAfterWaitingForLock(t *testing.T) {
	for _, op := range []string{"boot", "save", "on"} {
		t.Run(op, func(t *testing.T) {
			m, root := fixture(t, false)
			if err := m.entrySet(true); err != nil {
				t.Fatal(err)
			}
			s := getStatus(t, m)
			r := request(s)
			r.Targets = []Target{{"com.shouldnotwrite", "generate"}}
			other, err := newManager(root, module)
			if err != nil {
				t.Fatal(err)
			}
			defer other.close()
			other.list = m.list
			held, err := m.disk.lock()
			if err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				close(started)
				switch op {
				case "boot":
					done <- other.boot()
				case "save":
					done <- other.save(r)
				case "on":
					done <- other.entrySet(true)
				}
			}()
			<-started
			// Keep the lock until uninstall has marked stopping and removed our links.
			select {
			case err := <-done:
				held.Close()
				t.Fatalf("lock was bypassed: %v", err)
			case <-time.After(25 * time.Millisecond):
			}
			put(t, root, "/data/adb/keybox-lite/stopping", "")
			if err = m.cleanupLocked(); err != nil {
				held.Close()
				t.Fatal(err)
			}
			stateBefore := read(t, root, statePath)
			held.Close()
			select {
			case err := <-done:
				if op == "boot" && err != nil {
					t.Fatal(err)
				}
				if op != "boot" && (err == nil || !strings.Contains(err.Error(), "stopping")) {
					t.Fatalf("%s: %v", op, err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("lock was not released")
			}
			absent(t, root, entryPath)
			absent(t, root, configPath)
			if read(t, root, statePath) != stateBefore {
				t.Fatal("waiting operation wrote after cleanup")
			}
		})
	}
}
func TestDisableRemoveGatesAllowOffAndCleanup(t *testing.T) {
	for _, flag := range []string{"disable", "remove"} {
		m, root := fixture(t, false)
		if err := m.entrySet(true); err != nil {
			t.Fatal(err)
		}
		s := getStatus(t, m)
		put(t, root, module+"/"+flag, "")
		if err := m.save(request(s)); err == nil {
			t.Fatal("save allowed while " + flag)
		}
		if err := m.entrySet(true); err == nil {
			t.Fatal("enable allowed while " + flag)
		}
		if err := m.boot(); err != nil {
			t.Fatal(err)
		}
		if err := m.entrySet(false); err != nil {
			t.Fatal(err)
		}
		absent(t, root, entryPath)
		if err := m.cleanup(); err != nil {
			t.Fatal(err)
		}
	}
}
func TestEntryRejectsSymlinkParentAndDoesNotTouchModulesUpdate(t *testing.T) {
	m, root := fixture(t, false)
	outside := t.TempDir()
	symlink(t, root, "/data/adb/modules/oh_my_keymint", outside)
	if err := m.entrySet(true); err == nil {
		t.Fatal("foreign parent symlink accepted")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatal("escaped root")
	}
	os.Remove(filepath.Join(root, "/data/adb/modules/oh_my_keymint"))
	put(t, root, "/data/adb/modules_update/tricky_store/action.sh", "update action")
	if err := m.entrySet(true); err != nil {
		t.Fatal(err)
	}
	if err := m.entrySet(false); err != nil {
		t.Fatal(err)
	}
	if read(t, root, "/data/adb/modules_update/tricky_store/action.sh") != "update action" {
		t.Fatal("modified update action")
	}
}
