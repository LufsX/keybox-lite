package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"keyboxlite/internal/keybox"
)

func filesPrivateWorkClean(t *testing.T, e *engine) {
	t.Helper()
	entries, err := os.ReadDir(filesPath(e, ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "work.") {
			t.Fatalf("verification work leaked: %s", entry.Name())
		}
	}
}

func TestFilesPrepareWorkReplacementCannotOverwriteTarget(t *testing.T) {
	e := fixture(t)
	id, p := filesNew(t, e, syntheticKeybox(t), true)
	e.d.prepare = func(_ context.Context, _ string, data []byte, work string) ([]byte, error) {
		if err := os.Rename(work, work+".moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(work, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(e.host("/data/adb/tricky_store/keybox.xml"), filepath.Join(work, "candidate.xml")); err != nil {
			t.Fatal(err)
		}
		return data, nil
	}
	e.d.verify = func(context.Context, string, string, io.Writer) int { return 2 }
	if err := e.filesInstall(context.Background(), id, p.Revision, false); err == nil {
		t.Fatal("replaced preparation work installed")
	}
	unchanged(t, e)
	filesPrivateWorkClean(t, e)
}

func TestFilesVerifierCandidateMutationCannotInstall(t *testing.T) {
	e := fixture(t)
	id, p := filesNew(t, e, syntheticKeybox(t), true)
	e.d.verify = func(_ context.Context, path, _ string, _ io.Writer) int {
		if err := os.WriteFile(path, []byte("OTHER VERIFIED BYTES"), 0600); err != nil {
			t.Fatal(err)
		}
		return 0
	}
	if err := e.filesInstall(context.Background(), id, p.Revision, false); err == nil {
		t.Fatal("installed bytes differ from the verifier input")
	}
	unchanged(t, e)
	filesPrivateWorkClean(t, e)
}

func TestFilesCandidateAndDirectoryIdentityChecked(t *testing.T) {
	data := syntheticKeybox(t)
	for _, mode := range []string{"prepare-existing", "prepare-symlink", "prepare-hardlink", "prepare-fifo", "verify-inode", "verify-hardlink", "verify-mode", "verify-work-directory", "verify-work-symlink"} {
		t.Run(mode, func(t *testing.T) {
			e := fixture(t)
			id, p := filesNew(t, e, data, true)
			prepare := e.d.prepare
			if strings.HasPrefix(mode, "prepare-") {
				e.d.prepare = func(ctx context.Context, kind string, raw []byte, work string) ([]byte, error) {
					candidate := filepath.Join(work, "candidate.xml")
					var err error
					switch mode {
					case "prepare-existing":
						err = os.WriteFile(candidate, raw, 0600)
					case "prepare-symlink":
						err = os.Symlink(e.host("/data/adb/tricky_store/keybox.xml"), candidate)
					case "prepare-hardlink":
						err = os.Link(e.host("/data/adb/tricky_store/keybox.xml"), candidate)
					case "prepare-fifo":
						err = syscall.Mkfifo(candidate, 0600)
					}
					if err != nil {
						t.Fatal(err)
					}
					return prepare(ctx, kind, raw, work)
				}
				e.d.verify = func(context.Context, string, string, io.Writer) int {
					t.Error("pre-existing candidate reached verify")
					return 0
				}
			} else {
				e.d.verify = func(_ context.Context, candidate, work string, _ io.Writer) int {
					switch mode {
					case "verify-inode":
						content := read(t, candidate)
						if err := os.Remove(candidate); err != nil {
							t.Fatal(err)
						}
						mustWrite(t, candidate, []byte(content))
					case "verify-hardlink":
						if err := os.Link(candidate, candidate+".link"); err != nil {
							t.Fatal(err)
						}
					case "verify-mode":
						if err := os.Chmod(candidate, 0644); err != nil {
							t.Fatal(err)
						}
					case "verify-work-directory", "verify-work-symlink":
						if err := os.Rename(work, work+".moved"); err != nil {
							t.Fatal(err)
						}
						if mode == "verify-work-directory" {
							if err := os.Mkdir(work, 0700); err != nil {
								t.Fatal(err)
							}
							mustWrite(t, filepath.Join(work, "candidate.xml"), []byte(data))
						} else {
							if err := os.Symlink(work+".moved", work); err != nil {
								t.Fatal(err)
							}
						}
					}
					return 0
				}
			}
			if err := e.filesInstall(context.Background(), id, p.Revision, false); err == nil {
				t.Fatal("changed verification identity installed")
			}
			unchanged(t, e)
			filesPrivateWorkClean(t, e)
		})
	}
}

func TestFilesPrivateWorkRealPrepareVerifySuccess(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(fmt.Sprint(restore), func(t *testing.T) {
			e := fixture(t)
			realCommands(t, e)
			data := syntheticKeybox(t)
			setResponses(t, e, map[string]response{"https://android.googleapis.com/attestation/status": {Body: `{"entries":{}}`}})
			var id string
			var p Preview
			if restore {
				id = "keybox.xml.bak.123456"
				mustWrite(t, e.host("/data/adb/tricky_store/"+id), []byte(data))
				if err := filesCall(t, e, "", &p, "inspect", id); err != nil {
					t.Fatal(err)
				}
			} else {
				id, p = filesNew(t, e, data, true)
			}
			prepare := e.d.prepare
			verify := e.d.verify
			e.d.prepare = func(ctx context.Context, kind string, raw []byte, work string) ([]byte, error) {
				if !strings.HasPrefix(work, filesPath(e, "work.")) {
					t.Error("verification work is outside private state")
				}
				st, err := os.Stat(work)
				if err != nil || st.Mode().Perm() != 0700 {
					t.Error("work permissions not private")
				}
				return prepare(ctx, kind, raw, work)
			}
			e.d.verify = func(ctx context.Context, file, work string, out io.Writer) int {
				// Cleanup must not follow nested symlinks even when the real
				// verifier creates additional private temporary directories.
				nested := filepath.Join(work, "extra")
				if err := os.Mkdir(nested, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(e.host("/data/adb/tricky_store/keybox.xml"), filepath.Join(nested, "installed-link")); err != nil {
					t.Fatal(err)
				}
				return verify(ctx, file, work, out)
			}
			if err := e.filesInstall(context.Background(), id, p.Revision, restore); err != nil {
				t.Fatal(err)
			}
			got := read(t, e.host("/data/adb/tricky_store/keybox.xml"))
			if !keybox.StructureValid([]byte(got)) {
				t.Fatal("real verifier input not installed")
			}
			filesPrivateWorkClean(t, e)
			clean(t, e)
		})
	}
}

func TestFilesAbandonedPrivateWorkExpiresWithoutFollowingLinks(t *testing.T) {
	e := fixture(t)
	s, err := e.filesStore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	work, err := e.filesNewWork(s)
	s.close()
	if err != nil {
		t.Fatal(err)
	}
	stale := work.path
	mustWrite(t, filepath.Join(stale, "candidate.xml"), []byte("private stale candidate"))
	outside := filepath.Join(e.p.root, "outside-work")
	mustWrite(t, filepath.Join(outside, "unchanged"), []byte("untouched"))
	if err := os.Symlink(outside, filepath.Join(stale, "link")); err != nil {
		t.Fatal(err)
	}
	expired := e.d.now().Add(-filesTTL - time.Second)
	if err := os.Chtimes(stale, expired, expired); err != nil {
		t.Fatal(err)
	}
	// Simulate an interrupted process by closing descriptors without cleanup.
	work.dir.Close()
	work.parent.Close()
	unsafe := filesPath(e, "work."+strings.Repeat("e", 64))
	if err := os.Symlink(outside, unsafe); err != nil {
		t.Fatal(err)
	}
	if err := filesCall(t, e, "", nil, "create"); err != nil {
		t.Fatal(err)
	}
	if exists(stale) || !exists(unsafe) || read(t, filepath.Join(outside, "unchanged")) != "untouched" {
		t.Fatal("abandoned work cleanup followed a link or retained old private data")
	}
}

func TestFilesPreviewPreparesRawBeforeCertificateReports(t *testing.T) {
	e := fixture(t)
	_, p := filesNew(t, e, "\ufeff<!-- cleaned -->"+syntheticKeybox(t), true)
	if !p.Compatible || !p.StructureValid || p.Validity == nil || p.Integrity == nil || len(p.Serials) == 0 {
		t.Fatalf("prepareable input not previewed: %+v", p)
	}
}

func TestFilesPreviewRejectsGiantSerialBeforeExtraction(t *testing.T) {
	e := fixture(t)
	tlv := func(tag byte, body []byte) []byte {
		n := len(body)
		length := []byte{byte(n)}
		if n >= 128 {
			length = nil
			for remaining := n; remaining != 0; remaining >>= 8 {
				length = append([]byte{byte(remaining)}, length...)
			}
			length = append([]byte{0x80 | byte(len(length))}, length...)
		}
		return append(append([]byte{tag}, length...), body...)
	}
	der := tlv(0x30, tlv(0x30, tlv(2, bytes.Repeat([]byte{1}, 262144))))
	data := "<AndroidAttestation><Certificate>" + string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) + "</Certificate></AndroidAttestation>"
	if !keybox.StructureValid([]byte(data)) {
		t.Fatal("regression fixture must pass the structural XML check")
	}
	if _, err := keybox.Prepare([]byte(data), "tricky-store"); err == nil {
		t.Fatal("regression fixture must fail real Prepare")
	}
	id, _ := filesNew(t, e, data, false)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestFilesIsolatedProcess$", "--", "inspect", id)
	cmd.Env = append(os.Environ(), "KBL_FILES_PROCESS_ROOT="+e.p.root)
	raw, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("preview did not reject the malformed giant serial within 2 seconds")
	}
	if err != nil {
		t.Fatalf("inspect failed: %v: %s", err, raw)
	}
	var p Preview
	if err := json.Unmarshal(raw, &p); err != nil || p.Compatible || p.Error == "" || len(p.Serials) != 0 || p.Validity != nil || p.Integrity != nil {
		t.Fatalf("incompatible data reached certificate extraction: %+v %v", p, err)
	}
}

func TestFilesInstallRefreshesSessionBeforeVerification(t *testing.T) {
	for _, scenario := range []struct {
		code    int
		advance time.Duration
	}{{0, 2 * time.Second}, {2, 2 * time.Second}, {0, filesBudget - time.Second}, {2, filesBudget - time.Second}} {
		code := scenario.code
		t.Run(fmt.Sprintf("%d/%s", code, scenario.advance), func(t *testing.T) {
			e := fixture(t)
			now := time.Now()
			e.d.now = func() time.Time { return now }
			id, p := filesNew(t, e, syntheticKeybox(t), true)
			age := now.Add(-filesTTL + time.Second)
			if err := os.Chtimes(filesPath(e, id), age, age); err != nil {
				t.Fatal(err)
			}
			e.d.verify = func(context.Context, string, string, io.Writer) int {
				now = now.Add(scenario.advance)
				other := production(e.p.module)
				other.p = e.p
				other.d.now = e.d.now
				other.out = io.Discard
				if err := other.files(context.Background(), []string{"create"}, strings.NewReader("")); err != nil {
					t.Fatal(err)
				}
				if !exists(filesPath(e, id)) || !exists(filesPath(e, id+".sealed")) {
					t.Error("another command expired the active import")
				}
				return code
			}
			err := e.filesInstall(context.Background(), id, p.Revision, false)
			if code == 0 {
				if err != nil {
					t.Fatalf("active session expired during install: %v", err)
				}
				if exists(filesPath(e, id)) || exists(filesPath(e, id+".sealed")) {
					t.Fatal("successful import retained session")
				}
			} else {
				if err == nil || !exists(filesPath(e, id)) || !exists(filesPath(e, id+".sealed")) {
					t.Fatal("verification failure did not leave a retryable session")
				}
				unchanged(t, e)
				e.d.verify = func(context.Context, string, string, io.Writer) int { return 0 }
				if err := e.filesInstall(context.Background(), id, p.Revision, false); err != nil {
					t.Fatalf("failed import cannot be retried: %v", err)
				}
			}
			filesPrivateWorkClean(t, e)
		})
	}
}
