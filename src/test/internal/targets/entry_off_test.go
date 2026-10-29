package targets

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEntryOffAndRemoveCLIRecoverConflictsAndPreserveNativeWebUI(t *testing.T) {
	for _, command := range [][]string{{"entry", "set", "off"}, {"entry", "remove"}} {
		t.Run(strings.Join(command, "-"), func(t *testing.T) {
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
			pendingJournal(t, m, []change{{actionPath, action, snapshot{}}, {entryPath, web, snapshot{}}})
			if err = os.Remove(filepath.Join(root, entryPath)); err != nil {
				t.Fatal(err)
			}
			put(t, root, entryPath+"/index.html", "native upgraded webui")
			put(t, root, configPath, "[target]\ncom.external\n")
			args := append([]string{"--root", root, "--module-dir", module}, command...)
			var stdout, stderr bytes.Buffer
			if code := run(args, strings.NewReader(""), &stdout, &stderr); code != 0 || strings.TrimSpace(stdout.String()) != "{\"ok\":true}" {
				t.Fatalf("off failed: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			absent(t, root, actionPath)
			absent(t, root, stateDir+"/transaction.json")
			if read(t, root, entryPath+"/index.html") != "native upgraded webui" {
				t.Fatal("native WebUI changed")
			}
			if read(t, root, configPath) != "[target]\ncom.external\n" {
				t.Fatal("external backend config changed")
			}
			st, _, err := m.loadState()
			if err != nil || st.Entry {
				t.Fatalf("off was not persisted: %+v %v", st, err)
			}
			if err = m.boot(); err != nil {
				t.Fatal(err)
			}
			absent(t, root, actionPath)
			if read(t, root, entryPath+"/index.html") != "native upgraded webui" {
				t.Fatal("boot replayed stale entry")
			}
			stdout.Reset()
			stderr.Reset()
			if code := run(args, strings.NewReader(""), &stdout, &stderr); code != 0 {
				t.Fatalf("repeat off failed: %s", stderr.String())
			}
		})
	}
}
func TestEntryExplicitOffReportsCorruptStateAfterUnlinkButCleanupAllowsIt(t *testing.T) {
	for _, command := range [][]string{{"entry", "set", "off"}, {"entry", "remove"}} {
		t.Run(strings.Join(command, "-"), func(t *testing.T) {
			m, root := fixture(t, false)
			os.MkdirAll(filepath.Join(root, "/data/adb/magisk"), 0755)
			if err := m.entrySet(true); err != nil {
				t.Fatal(err)
			}
			put(t, root, statePath, "{broken-state")
			var stdout, stderr bytes.Buffer
			args := append([]string{"--root", root, "--module-dir", module}, command...)
			if code := run(args, strings.NewReader(""), &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "cannot persist disabled entry state") {
				t.Fatalf("state error hidden: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			absent(t, root, entryPath)
			absent(t, root, actionPath)
			if read(t, root, statePath) != "{broken-state" {
				t.Fatal("bad state overwritten")
			}
			// Recreate our links to verify uninstall uses the same path while tolerating
			// the unreadable settings that explicit user-facing off must report.
			symlink(t, root, entryPath, module+"/webroot")
			symlink(t, root, actionPath, module+"/webui.sh")
			if err := m.cleanup(); err != nil {
				t.Fatalf("uninstall blocked by corrupt settings: %v", err)
			}
			absent(t, root, entryPath)
			absent(t, root, actionPath)
			if read(t, root, statePath) != "{broken-state" {
				t.Fatal("uninstall overwrote damaged state")
			}
		})
	}
}
