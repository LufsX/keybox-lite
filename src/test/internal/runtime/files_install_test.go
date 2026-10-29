package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"keyboxlite/internal/backend"
	"keyboxlite/internal/keybox"
)

func filesInstalledClean(t *testing.T, e *engine) {
	t.Helper()
	clean(t, e)
	filesPrivateWorkClean(t, e)
	target := e.host("/data/adb/tricky_store/keybox.xml")
	for _, pattern := range []string{target + ".new.*", target + ".bak.*"} {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) != 0 {
			t.Fatalf("failed install leaked files: %v %v", matches, err)
		}
	}
}

func TestFilesInstallFailureRetainsTarget(t *testing.T) {
	data := syntheticKeybox(t)
	for _, restore := range []bool{false, true} {
		for _, mode := range []string{"prepare", "empty", "large-prepared", "verify1", "verify2", "verify3", "verify7", "verify-negative", "revision", "input-change", "input-symlink", "input-hardlink", "input-fifo", "backend-before", "backend-during", "metadata-during", "disable", "cancel", "timeout", "copy", "rename", "final-target"} {
			t.Run(fmt.Sprintf("restore=%t/%s", restore, mode), func(t *testing.T) {
				e := fixture(t)
				e.d.prepare = func(_ context.Context, kind string, raw []byte, _ string) ([]byte, error) {
					return keybox.Prepare(raw, kind)
				}
				target, err := e.target()
				if err != nil {
					t.Fatal(err)
				}
				var id, revision string
				if restore {
					id = "keybox.xml.bak.123"
					path := filepath.Join(filepath.Dir(e.host(target.KeyboxPath)), id)
					mustWrite(t, path, []byte(data))
					for _, preview := range filesBackupsCall(t, e).Backups {
						if preview.ID == id {
							revision = preview.Revision
						}
					}
				} else {
					var p Preview
					id, p = filesNew(t, e, data, true)
					revision = p.Revision
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				candidate := filesPath(e, id)
				if restore {
					candidate = filepath.Join(filepath.Dir(e.host(target.KeyboxPath)), id)
				}
				switch mode {
				case "prepare":
					e.d.prepare = func(context.Context, string, []byte, string) ([]byte, error) { return nil, fmt.Errorf("PRIVATE XML") }
				case "empty":
					e.d.prepare = func(context.Context, string, []byte, string) ([]byte, error) { return nil, nil }
				case "large-prepared":
					e.d.prepare = func(context.Context, string, []byte, string) ([]byte, error) {
						return bytes.Repeat([]byte("x"), filesMaxBytes+1), nil
					}
				case "verify1", "verify2", "verify3", "verify7":
					rc := int(mode[len(mode)-1] - '0')
					e.d.verify = func(context.Context, string, string, io.Writer) int { return rc }
				case "verify-negative":
					e.d.verify = func(context.Context, string, string, io.Writer) int { return -1 }
				case "revision":
					revision = strings.Repeat("0", 64)
					e.d.verify = func(context.Context, string, string, io.Writer) int { t.Error("stale revision verified"); return 0 }
				case "input-change":
					e.d.verify = func(context.Context, string, string, io.Writer) int {
						mustWrite(t, candidate, []byte(data+" "))
						return 0
					}
				case "input-symlink", "input-hardlink", "input-fifo":
					e.d.verify = func(context.Context, string, string, io.Writer) int {
						outside := filepath.Join(e.p.root, "replacement")
						mustWrite(t, outside, []byte(data))
						if err := os.Remove(candidate); err != nil {
							t.Fatal(err)
						}
						switch mode {
						case "input-symlink":
							if err := os.Symlink(outside, candidate); err != nil {
								t.Fatal(err)
							}
						case "input-hardlink":
							if err := os.Link(outside, candidate); err != nil {
								t.Fatal(err)
							}
						case "input-fifo":
							if err := syscall.Mkfifo(candidate, 0600); err != nil {
								t.Fatal(err)
							}
						}
						return 0
					}
				case "backend-before":
					changed := target
					changed.Backend = backend.TrickyStoreLegacy
					e.d.detect = func(string) (backend.Report, error) { return backend.Report{Selected: &changed}, nil }
					e.d.verify = func(context.Context, string, string, io.Writer) int { t.Error("changed backend verified"); return 0 }
				case "backend-during", "metadata-during":
					e.d.verify = func(context.Context, string, string, io.Writer) int {
						changed := target
						if mode == "backend-during" {
							changed.KeyboxPath = "/data/adb/tricky_store/other.xml"
						} else {
							changed.Version = "changed backend installation"
						}
						e.d.detect = func(string) (backend.Report, error) { return backend.Report{Selected: &changed}, nil }
						return 0
					}
				case "disable":
					e.d.verify = func(context.Context, string, string, io.Writer) int {
						mustWrite(t, filepath.Join(e.p.module, "disable"), nil)
						return 0
					}
				case "cancel":
					e.d.verify = func(context.Context, string, string, io.Writer) int { cancel(); return 0 }
				case "timeout":
					var timeoutCancel context.CancelFunc
					ctx, timeoutCancel = context.WithTimeout(ctx, 80*time.Millisecond)
					defer timeoutCancel()
					e.d.verify = func(ctx context.Context, _ string, _ string, _ io.Writer) int { <-ctx.Done(); return 0 }
				case "copy":
					e.d.copy = func(string, *os.File) error { return fmt.Errorf("copy failure") }
				case "rename":
					e.d.rename = func(string, string) error { return fmt.Errorf("rename failure") }
				case "final-target":
					calls := 0
					e.d.detect = func(string) (backend.Report, error) {
						calls++
						changed := target
						if calls >= 4 {
							changed.KeyboxPath += ".changed"
						}
						return backend.Report{Selected: &changed}, nil
					}
				}
				err = e.filesInstall(ctx, id, revision, restore)
				if err == nil || strings.Contains(err.Error(), "PRIVATE XML") {
					t.Fatalf("unsafe successful install: %v", err)
				}
				unchanged(t, e)
				if restore {
					// The source backup is retained; failed staging adds no backup.
					matches, _ := filepath.Glob(e.host(target.KeyboxPath) + ".bak.*")
					if len(matches) != 1 || filepath.Base(matches[0]) != id {
						t.Fatalf("restore failure damaged backups: %v", matches)
					}
					matches, _ = filepath.Glob(e.host(target.KeyboxPath) + ".new.*")
					if len(matches) != 0 {
						t.Fatal("staging leaked")
					}
				} else {
					filesInstalledClean(t, e)
				}
			})
		}
	}
}

func TestFilesInstallRequiresInspection(t *testing.T) {
	e := fixture(t)
	data := syntheticKeybox(t)
	id, _ := filesNew(t, e, data, false)
	tgt, _ := e.target()
	e.d.verify = func(context.Context, string, string, io.Writer) int { t.Error("uninspected import verified"); return 0 }
	if err := e.filesInstall(context.Background(), id, filesRevision(tgt, []byte(data)), false); err == nil {
		t.Fatal("uninspected import installed")
	}
	unchanged(t, e)
}

func TestFilesInstallAndRestoreSuccessPreservesBackup(t *testing.T) {
	data := syntheticKeybox(t)
	for _, restore := range []bool{false, true} {
		t.Run(fmt.Sprintf("restore=%t", restore), func(t *testing.T) {
			e := fixture(t)
			tgt, _ := e.target()
			target := e.host(tgt.KeyboxPath)
			os.Chmod(target, 0640)
			e.d.prepare = func(_ context.Context, kind string, raw []byte, _ string) ([]byte, error) {
				return keybox.Prepare(raw, kind)
			}
			var id, revision string
			if restore {
				id = "keybox.xml.bak.123"
				mustWrite(t, target+".bak.123", []byte("\ufeff<!-- remove -->"+data))
				for _, p := range filesBackupsCall(t, e).Backups {
					if p.ID == id {
						revision = p.Revision
					}
				}
			} else {
				var p Preview
				id, p = filesNew(t, e, "\ufeff<!-- remove -->"+data, true)
				revision = p.Revision
			}
			e.skipRevocation = true
			verifyCalled := false
			e.d.verify = func(ctx context.Context, path, work string, out io.Writer) int {
				verifyCalled = true
				if e.skipRevocation {
					t.Error("manual file used skip-revocation")
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > filesBudget {
					t.Error("verification missing 20 minute budget")
				}
				st, err := os.Stat(work)
				if err != nil || st.Mode().Perm() != 0700 {
					t.Error("work directory not private")
				}
				st, err = os.Stat(path)
				if err != nil || st.Mode().Perm() != 0600 {
					t.Error("verification input not private")
				}
				if strings.Contains(read(t, path), "remove") {
					t.Error("prepare was skipped")
				}
				fmt.Fprintln(out, "PRIVATE XML must not appear in public logs")
				return 0
			}
			e.out = &bytes.Buffer{}
			if err := e.filesInstall(context.Background(), id, revision, restore); err != nil {
				t.Fatalf("%v: %s", err, e.out)
			}
			if !verifyCalled || !e.skipRevocation {
				t.Fatal("strict policy was not scoped to the operation")
			}
			got := read(t, target)
			if !keybox.StructureValid([]byte(got)) || strings.Contains(got, "remove") {
				t.Fatal("prepared content was not installed")
			}
			st, _ := os.Stat(target)
			if st.Mode().Perm() != 0640 {
				t.Fatal("metadata not preserved")
			}
			if strings.Contains(e.out.(*bytes.Buffer).String(), "PRIVATE XML") || !strings.Contains(e.out.(*bytes.Buffer).String(), "[DONE]") {
				t.Fatal("unsafe or incomplete output")
			}
			backups, _ := filepath.Glob(target + ".bak.*")
			oldFound := false
			for _, path := range backups {
				oldFound = oldFound || read(t, path) == "OLD"
			}
			if !oldFound {
				t.Fatal("existing installed keybox was not backed up")
			}
			if restore && !exists(target+".bak.123") {
				t.Fatal("restored source backup removed unexpectedly")
			}
			if !restore && (exists(filesPath(e, id)) || exists(filesPath(e, id+".sealed"))) {
				t.Fatal("successful import retained private session")
			}
			clean(t, e)
		})
	}
}

func TestFilesStrictRevocationIgnoresSavedSkip(t *testing.T) {
	data := syntheticKeybox(t)
	for _, restore := range []bool{false, true} {
		t.Run(fmt.Sprintf("restore=%t", restore), func(t *testing.T) {
			e := fixture(t)
			realCommands(t, e)
			if err := e.init(); err != nil {
				t.Fatal(err)
			}
			if err := e.writeConfig(config{source: "meow", skipRevocation: true}); err != nil {
				t.Fatal(err)
			}
			e.skipRevocation = true
			setResponses(t, e, map[string]response{"https://android.googleapis.com/attestation/status": {Body: `{"entries":{"2a":{"status":"REVOKED","reason":"KEY_COMPROMISE"}}}`}})
			var id, revision string
			if restore {
				id = "keybox.xml.bak.999"
				mustWrite(t, e.host("/data/adb/tricky_store/keybox.xml")+".bak.999", []byte(data))
				revision = filesBackupsCall(t, e).Backups[0].Revision
			} else {
				var p Preview
				id, p = filesNew(t, e, data, true)
				revision = p.Revision
			}
			err := e.filesInstall(context.Background(), id, revision, restore)
			if err == nil || !strings.Contains(err.Error(), "exit 1") {
				t.Fatalf("revoked keybox installed or check skipped: %v", err)
			}
			unchanged(t, e)
			if !exists(filepath.Join(e.p.root, "requests")) {
				t.Fatal("strict file install never requested revocation status")
			}
		})
	}
}

func TestFilesRestoreRejectsUnsafeIDsAndFiles(t *testing.T) {
	e := fixture(t)
	target := e.host("/data/adb/tricky_store/keybox.xml")
	data := syntheticKeybox(t)
	mustWrite(t, target+".bak.12", []byte(data))
	revision := filesBackupsCall(t, e).Backups[0].Revision
	for _, id := range []string{"../keybox.xml.bak.12", target + ".bak.12", "other.xml.bak.12", "keybox.xml.bak.", "keybox.xml.bak.manual", "keybox.xml.bak.12/../keybox.xml", "keybox.xml"} {
		if err := e.filesInstall(context.Background(), id, revision, true); err == nil {
			t.Fatalf("unsafe backup ID installed: %s", id)
		}
	}
	os.Remove(target + ".bak.12")
	os.Symlink(target, target+".bak.12")
	if err := e.filesInstall(context.Background(), "keybox.xml.bak.12", revision, true); err == nil {
		t.Fatal("backup symlink restored")
	}
	os.Remove(target + ".bak.12")
	mustWrite(t, target+".bak.12", []byte(data))
	os.Link(target+".bak.12", target+".bak.13")
	if err := e.filesInstall(context.Background(), "keybox.xml.bak.12", revision, true); err == nil {
		t.Fatal("backup hardlink restored")
	}
	unchanged(t, e)
}

func TestFilesConcurrentVerificationAllowsDiscardAndBlocksUpdate(t *testing.T) {
	e := fixture(t)
	id, p := filesNew(t, e, syntheticKeybox(t), true)
	entered := make(chan struct{})
	proceed := make(chan struct{})
	e.d.verify = func(context.Context, string, string, io.Writer) int {
		close(entered)
		<-proceed
		return 0
	}
	done := make(chan error, 1)
	go func() { done <- e.filesInstall(context.Background(), id, p.Revision, false) }()
	<-entered
	other := production(e.p.module)
	other.p = e.p
	other.out = io.Discard
	if err := other.update(context.Background()); err == nil {
		t.Error("update bypassed manual install action.lock")
	}
	if err := other.files(context.Background(), []string{"discard", id}, strings.NewReader("")); err != nil {
		t.Error(err)
	}
	close(proceed)
	if err := <-done; err == nil {
		t.Fatal("discarded session installed")
	}
	unchanged(t, e)
	filesInstalledClean(t, e)
	// The failed action releases the common install lock.
	f, err := lock(context.Background(), filepath.Join(e.p.state, "action.lock"), 0)
	if err != nil {
		t.Fatal("failed action retained lock")
	}
	f.Close()
}

func TestFilesBackupLimitAfterSuccess(t *testing.T) {
	e := fixture(t)
	target := e.host("/data/adb/tricky_store/keybox.xml")
	for i := 1; i <= 5; i++ {
		path := fmt.Sprintf("%s.bak.%d", target, i)
		mustWrite(t, path, []byte("old backup"))
		os.Chtimes(path, time.Unix(int64(i), 0), time.Unix(int64(i), 0))
	}
	id, p := filesNew(t, e, syntheticKeybox(t), true)
	if err := e.filesInstall(context.Background(), id, p.Revision, false); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(target + ".bak.*")
	if len(backups) != backupLimit {
		t.Fatalf("backup policy changed: %d", len(backups))
	}
	found := false
	for _, path := range backups {
		found = found || read(t, path) == "OLD"
	}
	if !found {
		t.Fatal("newest old-keybox backup pruned")
	}
}

func TestFilesRealVerifierCancellationCleansDescendants(t *testing.T) {
	e := fixture(t)
	realCommands(t, e)
	t.Setenv("KBL_HELPER_VERIFY_WAIT", "1")
	id, p := filesNew(t, e, syntheticKeybox(t), true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- e.filesInstall(ctx, id, p.Revision, false) }()
	deadline := time.Now().Add(4 * time.Second)
	for !exists(filepath.Join(e.p.root, "verify-child")) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !exists(filepath.Join(e.p.root, "verify-child")) {
		cancel()
		t.Fatal("verifier did not start")
	}
	pid := read(t, filepath.Join(e.p.root, "verify-child"))
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled verification installed")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("verifier did not terminate")
	}
	// Existing runtime process helper treats zombies as terminated.
	raw, err := os.ReadFile(filepath.Join("/proc", pid, "stat"))
	if err == nil {
		end := strings.LastIndex(string(raw), ") ")
		if end < 0 || !strings.Contains("ZX", string(raw[end+2])) {
			t.Fatal("verifier descendant survived cancellation")
		}
	}
	unchanged(t, e)
	filesInstalledClean(t, e)
}
