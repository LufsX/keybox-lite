package targets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupRecoversInterruptedLegacySaveAsCompleteConfiguration(t *testing.T) {
	for _, committed := range []int{1, 2, 3} {
		t.Run(string(rune('0'+committed))+"-files-committed", func(t *testing.T) {
			m, root := fixture(t, true)
			put(t, root, targetPath, "# original targets\ncom.old!\nuid:10123?\n")
			put(t, root, patchPath, "# original policy\nsystem=202501\nvendor=20250101\nboot=prop\nfuture=keep\n")
			os.MkdirAll(filepath.Join(root, "/data/adb/magisk"), 0755)
			if err := m.entrySet(true); err != nil {
				t.Fatal(err)
			}
			targetBefore := read(t, root, targetPath)
			policyBefore := read(t, root, patchPath)
			st, raw, err := m.loadState()
			if err != nil {
				t.Fatal(err)
			}
			originalSystems := append([]string{}, st.SystemApps...)
			doc, err := readDocument(m.disk, true)
			if err != nil {
				t.Fatal(err)
			}
			c := Config{Targets: []Target{{"com.incomplete", "generate"}}, DefaultPolicy: Policy{"os_patch": "202610", "vendor_patch": "no", "boot_patch": "20261001"}, PerAppPolicy: map[string]Policy{}}
			changes := doc.changes(c)
			st.SystemApps = []string{"com.changed.system"}
			st.Auto = true
			st.Baseline = true
			st.Known = []string{"com.baseline"}
			sc, err := stateChange(st, raw)
			if err != nil {
				t.Fatal(err)
			}
			changes = append(changes, sc)
			pendingJournal(t, m, changes)
			// Exercise every interrupted commit boundary, including all three writes
			// committed but the journal's removal not yet completed.
			for _, change := range changes[:committed] {
				if err = m.disk.write(change.Path, change.After); err != nil {
					t.Fatal(err)
				}
			}
			if err = m.cleanup(); err != nil {
				t.Fatal(err)
			}
			if read(t, root, targetPath) != targetBefore || read(t, root, patchPath) != policyBefore {
				t.Fatal("legacy target and patch files did not recover together")
			}
			recovered, _, err := m.loadState()
			if err != nil {
				t.Fatal(err)
			}
			if recovered.Auto || recovered.Baseline || len(recovered.Known) != 0 || recovered.Entry {
				t.Fatalf("baseline/state not recovered and entry disabled: %+v", recovered)
			}
			if len(recovered.SystemApps) != len(originalSystems) {
				t.Fatal("system-app state did not recover")
			}
			for i, p := range recovered.SystemApps {
				if p != originalSystems[i] {
					t.Fatal("system-app state changed")
				}
			}
			absent(t, root, entryPath)
			absent(t, root, actionPath)
			absent(t, root, stateDir+"/transaction.json")
			if err = m.cleanup(); err != nil {
				t.Fatal(err)
			}
			if read(t, root, targetPath) != targetBefore || read(t, root, patchPath) != policyBefore {
				t.Fatal("repeat cleanup replayed transaction")
			}
		})
	}
}
