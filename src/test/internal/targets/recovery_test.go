package targets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinalCommitFailureRollsBackAndRetryRecoversPendingRollback(t *testing.T) {
	for _, pending := range []bool{false, true} {
		m, root := fixture(t, false)
		put(t, root, configPath, "[target]\ncom.old\n")
		s := getStatus(t, m)
		r := request(s)
		r.Targets = []Target{{"com.next", "hack"}}
		failedCommit, failedRollback := false, false
		m.disk.beforeCommit = func(p string, w snapshot) error {
			if p == stateDir+"/transaction.json" && !w.Exists && !failedCommit {
				failedCommit = true
				return errors.New("commit marker removal failed")
			}
			if pending && failedCommit && !failedRollback && p == configPath {
				failedRollback = true
				return errors.New("rollback disk error")
			}
			return nil
		}
		err := m.save(r)
		if err == nil || !strings.Contains(err.Error(), "commit marker removal failed") {
			t.Fatalf("%v", err)
		}
		if pending {
			if !strings.Contains(err.Error(), "rollback pending") {
				t.Fatalf("%v", err)
			}
			if !strings.Contains(read(t, root, configPath), "com.next?") {
				t.Fatal("failure did not exercise partial rollback")
			}
		} else if read(t, root, configPath) != "[target]\ncom.old\n" {
			t.Fatal("commit failure did not roll back")
		}
		m.disk.beforeCommit = nil
		// Next operation finishes any pending rollback before exposing configuration.
		if s = getStatus(t, m); len(s.Config.Targets) != 1 || s.Config.Targets[0].PackageName != "com.old" {
			t.Fatalf("%+v", s.Config)
		}
		absent(t, root, statePath)
		absent(t, root, stateDir+"/transaction.json")
		if err = m.save(request(s)); err != nil {
			t.Fatalf("retry: %v", err)
		}
	}
}
func TestAutoStateCommitFailureDoesNotLoseNewApp(t *testing.T) {
	m, root := fixture(t, true)
	packages := map[string]bool{"com.old": false}
	m.list = func() (map[string]bool, error) { return packages, nil }
	r := request(getStatus(t, m))
	r.AutoAddNewApps = true
	if err := m.save(r); err != nil {
		t.Fatal(err)
	}
	beforeTarget := read(t, root, targetPath)
	beforeState := read(t, root, statePath)
	beforePatch := read(t, root, patchPath)
	packages["com.new"] = false
	fail := true
	m.disk.beforeCommit = func(p string, w snapshot) error {
		if p == statePath && fail {
			fail = false
			return errors.New("auto state failure")
		}
		return nil
	}
	if err := m.auto(); err == nil {
		t.Fatal("auto failure not reported")
	}
	if read(t, root, targetPath) != beforeTarget || read(t, root, statePath) != beforeState || read(t, root, patchPath) != beforePatch {
		t.Fatal("auto target and baseline diverged")
	}
	m.disk.beforeCommit = nil
	if err := m.auto(); err != nil {
		t.Fatal(err)
	}
	targets := getStatus(t, m).Config.Targets
	if len(targets) != 1 || targets[0].PackageName != "com.new" {
		t.Fatal("new app lost after failed baseline commit")
	}
}
func TestBootRestoresEntryEvenWhenPMFails(t *testing.T) {
	m, root := fixture(t, false)
	r := request(getStatus(t, m))
	r.AutoAddNewApps = true
	if err := m.save(r); err != nil {
		t.Fatal(err)
	}
	if err := m.entrySet(true); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(root, entryPath))
	m.list = func() (map[string]bool, error) { return nil, errors.New("pm offline") }
	if err := m.boot(); err == nil {
		t.Fatal("auto error not propagated")
	}
	requireLink(t, root, entryPath, module+"/webroot")
}
func TestLockHardlinkCannotChangeOutsidePermissions(t *testing.T) {
	m, root := fixture(t, false)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, stateDir), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(root, stateDir, "lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.status(); err == nil {
		t.Fatal("hardlinked lock accepted")
	}
	st, err := os.Stat(outside)
	if err != nil || st.Mode().Perm() != 0644 {
		t.Fatal("outside hardlink permissions modified")
	}
}
func TestProtoNamesAndSingleSegmentPolicySection(t *testing.T) {
	for _, p := range []string{"constructor", "prototype", "__proto__", "constructor@1", "target", "default_policy"} {
		if validName(p) {
			t.Fatalf("unsafe map or section key %q accepted", p)
		}
	}
	m, root := fixture(t, false)
	put(t, root, configPath, "[single_app]\nboot_patch=20260230\n[target]\nsingle_app\n")
	if s := getStatus(t, m); s.ConfigError == "" {
		t.Fatal("invalid policy in app section before targets ignored")
	}
}
