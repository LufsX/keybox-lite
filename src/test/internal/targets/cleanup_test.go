package targets

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pendingJournal(t *testing.T, m *manager, changes []change) {
	t.Helper()
	data, err := json.Marshal(journal{Version: 1, Changes: changes})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.disk.write(stateDir+"/transaction.json", fileImage(data, snapshot{}, true)); err != nil {
		t.Fatal(err)
	}
}
func TestCleanupEntryConflictRetiresJournalAndPreservesReplacements(t *testing.T) {
	for _, kind := range []string{"directory", "file", "foreign-symlink", "dangling-symlink"} {
		t.Run(kind, func(t *testing.T) {
			m, root := fixture(t, false)
			os.MkdirAll(filepath.Join(root, "/data/adb/magisk"), 0755)
			if err := m.entrySet(true); err != nil {
				t.Fatal(err)
			}
			web, err := m.disk.read(entryPath, true)
			if err != nil {
				t.Fatal(err)
			}
			action, err := m.disk.read(actionPath, true)
			if err != nil {
				t.Fatal(err)
			}
			st, raw, err := m.loadState()
			if err != nil {
				t.Fatal(err)
			}
			st.Entry = false
			sc, err := stateChange(st, raw)
			if err != nil {
				t.Fatal(err)
			}
			// An interrupted "off" transaction removed webroot but not action.sh.
			// Its rollback would restore our links if Cleanup left the journal live.
			pendingJournal(t, m, []change{{actionPath, action, snapshot{}}, {entryPath, web, snapshot{}}, sc})
			if err = os.Remove(filepath.Join(root, entryPath)); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "directory":
				put(t, root, entryPath+"/index.html", "backend native WebUI")
			case "file":
				put(t, root, entryPath, "backend native file")
			case "foreign-symlink":
				put(t, root, "/foreign/index.html", "foreign WebUI")
				symlink(t, root, entryPath, filepath.Join(root, "foreign"))
			case "dangling-symlink":
				symlink(t, root, entryPath, "/foreign/missing")
			}
			external, externalNative, err := m.disk.lstatEntry(entryPath)
			if err != nil {
				t.Fatal(err)
			}
			put(t, root, configPath, "[target]\ncom.external.edit\n")
			// Confirm that the ordinary recovery gate really is blocked for this fixture.
			if err = m.locked(func() error { t.Fatal("conflicted recovery was bypassed"); return nil }); err == nil {
				t.Fatal("expected a recovery conflict")
			}
			// This is the same complete lock/journal path invoked by exported Cleanup;
			// unlike cleanupLocked it must acquire its own lock without the recovery gate.
			if err = m.cleanup(); err != nil {
				t.Fatal(err)
			}
			absent(t, root, actionPath)
			absent(t, root, stateDir+"/transaction.json")
			got, native, err := m.disk.lstatEntry(entryPath)
			if err != nil || native != externalNative || !same(got, external) {
				t.Fatal("external WebUI replaced during cleanup")
			}
			if kind == "directory" && read(t, root, entryPath+"/index.html") != "backend native WebUI" {
				t.Fatal("native directory contents changed")
			}
			if kind == "file" && read(t, root, entryPath) != "backend native file" {
				t.Fatal("native file contents changed")
			}
			if read(t, root, configPath) != "[target]\ncom.external.edit\n" {
				t.Fatal("external backend config overwritten")
			}
			if err = m.cleanup(); err != nil {
				t.Fatalf("repeated cleanup: %v", err)
			}
			// A later ordinary invocation must not replay the abandoned entry transaction.
			s := getStatus(t, m)
			if s.Entry.Enabled || s.Entry.Linked || !s.Entry.HasOwnWebUI {
				t.Fatalf("old entry restored: %+v", s.Entry)
			}
			absent(t, root, actionPath)
			absent(t, root, stateDir+"/transaction.json")
			got, native, err = m.disk.lstatEntry(entryPath)
			if err != nil || native != externalNative || !same(got, external) {
				t.Fatal("old journal replayed after cleanup")
			}
		})
	}
}
func TestCleanupRecoversUnchangedConfigAndPreservesExternalEdits(t *testing.T) {
	for _, externalEdit := range []bool{false, true} {
		m, root := fixture(t, false)
		put(t, root, configPath, "[target]\ncom.before\n")
		if err := m.entrySet(true); err != nil {
			t.Fatal(err)
		}
		before, err := m.disk.read(configPath, false)
		if err != nil {
			t.Fatal(err)
		}
		after := fileImage([]byte("[target]\ncom.transaction\n"), before, false)
		pendingJournal(t, m, []change{{configPath, before, after}})
		if err = m.disk.write(configPath, after); err != nil {
			t.Fatal(err)
		}
		want := string(before.Data)
		if externalEdit {
			want = "[target]\ncom.external\n"
			put(t, root, configPath, want)
		}
		if err = m.cleanup(); err != nil {
			t.Fatal(err)
		}
		absent(t, root, entryPath)
		absent(t, root, stateDir+"/transaction.json")
		if read(t, root, configPath) != want {
			t.Fatal("cleanup did not recover the unchanged transaction or preserve the external edit")
		}
		if err = m.cleanup(); err != nil {
			t.Fatal(err)
		}
		if s := getStatus(t, m); s.Entry.Enabled || s.Entry.Linked {
			t.Fatal("entry restored")
		}
		if read(t, root, configPath) != want {
			t.Fatal("stale journal replay changed backend configuration")
		}
	}
}
func TestCleanupJournalRemovalFailureStillUnlinksAndNeutralizesReplay(t *testing.T) {
	m, root := fixture(t, false)
	os.MkdirAll(filepath.Join(root, "/data/adb/magisk"), 0755)
	if err := m.entrySet(true); err != nil {
		t.Fatal(err)
	}
	web, err := m.disk.read(entryPath, true)
	if err != nil {
		t.Fatal(err)
	}
	action, err := m.disk.read(actionPath, true)
	if err != nil {
		t.Fatal(err)
	}
	pendingJournal(t, m, []change{{entryPath, web, snapshot{}}, {actionPath, action, snapshot{}}})
	m.disk.beforeCommit = func(p string, w snapshot) error {
		if p == stateDir+"/transaction.json" && !w.Exists {
			return errors.New("journal unlink failed")
		}
		return nil
	}
	err = m.cleanup()
	if err == nil || !strings.Contains(err.Error(), "retired pending journal") || !strings.Contains(err.Error(), "journal unlink failed") {
		t.Fatalf("cleanup diagnostic: %v", err)
	}
	absent(t, root, entryPath)
	absent(t, root, actionPath)
	// The fallback has removed all replayable changes even though unlink failed.
	journalData := read(t, root, stateDir+"/transaction.json")
	var j journal
	if err = json.Unmarshal([]byte(journalData), &j); err != nil || j.Version != 1 || len(j.Changes) != 0 {
		t.Fatalf("journal still replayable: %s (%v)", journalData, err)
	}
	m.disk.beforeCommit = nil
	if s := getStatus(t, m); s.Entry.Enabled || s.Entry.Linked {
		t.Fatal("stale transaction restored entry")
	}
	absent(t, root, entryPath)
	absent(t, root, actionPath)
	if err = m.cleanup(); err != nil {
		t.Fatal(err)
	}
}
func TestCleanupContinuesAfterIndividualUnlinkError(t *testing.T) {
	m, root := fixture(t, false)
	os.MkdirAll(filepath.Join(root, "/data/adb/magisk"), 0755)
	if err := m.entrySet(true); err != nil {
		t.Fatal(err)
	}
	m.disk.beforeCommit = func(p string, w snapshot) error {
		if p == entryPath {
			return errors.New("webroot unlink denied")
		}
		return nil
	}
	err := m.cleanup()
	if err == nil || !strings.Contains(err.Error(), entryPath) || !strings.Contains(err.Error(), "webroot unlink denied") {
		t.Fatalf("cleanup error: %v", err)
	}
	absent(t, root, actionPath)
	requireLink(t, root, entryPath, module+"/webroot")
	m.disk.beforeCommit = nil
	if err = m.cleanup(); err != nil {
		t.Fatal(err)
	}
	absent(t, root, entryPath)
}
