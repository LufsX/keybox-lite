package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"keyboxlite/internal/backend"
	"keyboxlite/internal/keybox"
)

func filesCall(t *testing.T, e *engine, stdin string, result any, args ...string) error {
	t.Helper()
	e.out = &bytes.Buffer{}
	err := e.files(context.Background(), args, strings.NewReader(stdin))
	if err == nil && result != nil {
		if decodeErr := json.Unmarshal(e.out.(*bytes.Buffer).Bytes(), result); decodeErr != nil {
			t.Fatalf("invalid JSON: %v: %s", decodeErr, e.out)
		}
	}
	return err
}

func filesNew(t *testing.T, e *engine, data string, inspect bool) (string, Preview) {
	t.Helper()
	var created struct{ ID string }
	if err := filesCall(t, e, "", &created, "create"); err != nil {
		t.Fatal(err)
	}
	if !filesID(created.ID) {
		t.Fatal("unsafe token")
	}
	for offset := 0; offset < len(data); {
		end := offset + filesChunkBytes
		if end > len(data) {
			end = len(data)
		}
		var reply struct {
			OK   bool
			Size int64
		}
		if err := filesCall(t, e, base64.StdEncoding.EncodeToString([]byte(data[offset:end])), &reply, "append", created.ID, strconv.Itoa(offset)); err != nil {
			t.Fatal(err)
		}
		if !reply.OK || reply.Size != int64(end) {
			t.Fatalf("append reply %+v", reply)
		}
		offset = end
	}
	var p Preview
	if inspect {
		if err := filesCall(t, e, "", &p, "inspect", created.ID); err != nil {
			t.Fatal(err)
		}
	}
	return created.ID, p
}

func filesPath(e *engine, id string) string {
	if id == "files.lock" {
		return filepath.Join(e.p.state, "locks", id)
	}
	return filepath.Join(e.p.state, "keybox-files", id)
}

func filesBackupsCall(t *testing.T, e *engine) filesBackups {
	t.Helper()
	var result filesBackups
	if err := filesCall(t, e, "", &result, "backups"); err != nil {
		t.Fatal(err)
	}
	if result.Backups == nil {
		t.Fatal("backups must be an array")
	}
	return result
}

func TestFilesPreviewAndSealed(t *testing.T) {
	e := fixture(t)
	data := syntheticKeybox(t)
	e.d.verify = func(context.Context, string, string, io.Writer) int {
		t.Fatal("preview attempted revocation download")
		return 0
	}
	id, p := filesNew(t, e, data, true)
	if !p.StructureValid || !p.Compatible || p.Error != "" || len(p.Serials) == 0 ||
		p.Validity == nil || p.Validity.Status != "valid" || p.Integrity == nil || p.Integrity.Status != "valid" ||
		p.Revocation != "not_checked" || !filesID(p.Revision) || p.Size != int64(len(data)) || p.Modified == 0 ||
		p.Backend.Backend != backend.TrickyStore || p.Target != "/data/adb/tricky_store/keybox.xml" {
		t.Fatalf("preview incorrect: %+v", p)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "PRIVATE KEY") || strings.Contains(string(raw), "DeviceID") {
		t.Fatal("preview exposed XML")
	}
	if err := filesCall(t, e, base64.StdEncoding.EncodeToString([]byte("x")), nil, "append", id, strconv.Itoa(len(data))); err == nil {
		t.Fatal("sealed session accepted append")
	}
	for name, mode := range map[string]os.FileMode{
		filesPath(e, ""):           0700,
		filesPath(e, id):           0600,
		filesPath(e, id+".sealed"): 0600,
		filesPath(e, "files.lock"): 0600,
	} {
		st, err := os.Stat(name)
		if err != nil || st.Mode().Perm() != mode {
			t.Fatalf("permissions %s: %v %v", name, st, err)
		}
	}
	var again Preview
	if err := filesCall(t, e, "", &again, "inspect", id); err != nil || again.Revision != p.Revision {
		t.Fatalf("repeat inspect: %v %+v", err, again)
	}
	for _, bad := range []string{"not XML PRIVATE SECRET", "<AndroidAttestation><bogus/></AndroidAttestation>"} {
		_, invalid := filesNew(t, e, bad, true)
		if invalid.Compatible || invalid.Error == "" || invalid.Revocation != "not_checked" {
			t.Fatalf("invalid preview: %+v", invalid)
		}
		raw, _ := json.Marshal(invalid)
		if bytes.Contains(raw, []byte("PRIVATE SECRET")) {
			t.Fatal("error exposed candidate data")
		}
	}
}

func TestFilesSafeBackupEnumeration(t *testing.T) {
	e := fixture(t)
	target := e.host("/data/adb/tricky_store/keybox.xml")
	data := syntheticKeybox(t)
	mustWrite(t, target+".bak.100", []byte(data))
	mustWrite(t, target+".bak.101", []byte("SECRET malformed XML"))
	os.Chtimes(target+".bak.100", time.Unix(100, 0), time.Unix(100, 0))
	os.Chtimes(target+".bak.101", time.Unix(101, 0), time.Unix(101, 0))
	mustWrite(t, target+".bak.manual", []byte(data))
	mustWrite(t, target+".bak.", []byte(data))
	if err := os.Symlink(target+".bak.100", target+".bak.102"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, target+".bak.103", []byte(data))
	if err := os.Link(target+".bak.103", target+".bak.104"); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(target+".bak.105", 0700)
	if err := syscall.Mkfifo(target+".bak.106", 0600); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, target+".bak.107", bytes.Repeat([]byte("X"), filesMaxBytes+1))
	result := filesBackupsCall(t, e)
	got := map[string]Preview{}
	for _, p := range result.Backups {
		got[p.ID] = p
	}
	if len(got) != 3 || !got["keybox.xml.bak.100"].Compatible || got["keybox.xml.bak.101"].StructureValid ||
		got["keybox.xml.bak.107"].Error == "" || got["keybox.xml.bak.107"].Revision != "" {
		t.Fatalf("unsafe enumeration: %+v", result)
	}
	var inspected Preview
	if err := filesCall(t, e, "", &inspected, "inspect", "keybox.xml.bak.100"); err != nil || inspected.Revision != got["keybox.xml.bak.100"].Revision {
		t.Fatalf("backup inspection mismatch: %+v %v", inspected, err)
	}
	for _, id := range []string{"other.xml.bak.100", "keybox.xml.bak.102", "keybox.xml.bak.103", "keybox.xml.bak.106", "keybox.xml.bak.107"} {
		if err := filesCall(t, e, "", nil, "inspect", id); err == nil {
			t.Fatalf("unsafe backup inspected: %s", id)
		}
	}
	for _, p := range result.Backups {
		if p.Revocation != "not_checked" {
			t.Fatal("backup falsely marked checked")
		}
	}
	if got["keybox.xml.bak.101"].Error == "" {
		t.Fatal("invalid XML missing error")
	}
	// A symlink in an ancestor is rejected as well.
	dir := filepath.Dir(target)
	real := dir + "-real"
	if err := os.Rename(dir, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, dir); err != nil {
		t.Fatal(err)
	}
	var ignored filesBackups
	if err := filesCall(t, e, "", &ignored, "backups"); err == nil {
		t.Fatal("ancestor symlink accepted")
	}
}

func TestFilesAppendBoundariesAndReplay(t *testing.T) {
	e := fixture(t)
	id, _ := filesNew(t, e, "", false)
	for _, tc := range []struct{ offset, encoded string }{
		{"1", base64.StdEncoding.EncodeToString([]byte("x"))},
		{"0", "?SECRET"},
		{"0", ""},
		{"0", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), filesChunkBytes+1))},
	} {
		if err := filesCall(t, e, tc.encoded, nil, "append", id, tc.offset); err == nil {
			t.Fatalf("bad append accepted: offset=%s bytes=%d", tc.offset, len(tc.encoded))
		}
		if read(t, filesPath(e, id)) != "" {
			t.Fatal("failed chunk changed file")
		}
	}
	payload := strings.Repeat("a", filesMaxBytes)
	id, _ = filesNew(t, e, payload, false)
	if err := filesCall(t, e, base64.StdEncoding.EncodeToString([]byte("x")), nil, "append", id, strconv.Itoa(filesMaxBytes)); err == nil {
		t.Fatal("total size limit ignored")
	}
	if err := filesCall(t, e, base64.StdEncoding.EncodeToString([]byte("x")), nil, "append", id, "0"); err == nil {
		t.Fatal("replay accepted")
	}
	if got := read(t, filesPath(e, id)); got != payload {
		t.Fatal("rejected append changed file")
	}
}

func TestFilesLimitExpiryAndDiscard(t *testing.T) {
	e := fixture(t)
	ids := []string{}
	for range filesSessionLimit {
		id, _ := filesNew(t, e, "not XML", true)
		ids = append(ids, id)
	}
	if err := filesCall(t, e, "", nil, "create"); err == nil {
		t.Fatal("active session limit ignored")
	}
	for _, id := range ids[:2] {
		expired := e.d.now().Add(-filesTTL - time.Second)
		if err := os.Chtimes(filesPath(e, id), expired, expired); err != nil {
			t.Fatal(err)
		}
	}
	// Orphaned seal records from an interrupted discard are removed too.
	orphan := strings.Repeat("a", 64)
	mustWrite(t, filesPath(e, orphan+".sealed"), []byte(strings.Repeat("0", 64)))
	filesNew(t, e, "", false)
	for _, id := range append(ids[:2], orphan) {
		if exists(filesPath(e, id)) || exists(filesPath(e, id+".sealed")) {
			t.Fatal("expired/orphan session retained")
		}
	}
	for range 2 {
		var reply struct{ OK bool }
		if err := filesCall(t, e, "", &reply, "discard", ids[2]); err != nil || !reply.OK {
			t.Fatalf("discard not idempotent: %v %+v", err, reply)
		}
	}
	if exists(filesPath(e, ids[2])) || exists(filesPath(e, ids[2]+".sealed")) {
		t.Fatal("discard retained private data")
	}
}

func TestFilesRejectUnsafeSessions(t *testing.T) {
	for _, mode := range []string{"symlink", "hardlink", "fifo", "directory", "oversize", "permissions", "sealed-link", "store-link", "lock-link"} {
		t.Run(mode, func(t *testing.T) {
			e := fixture(t)
			id, _ := filesNew(t, e, "original", false)
			path := filesPath(e, id)
			outside := filepath.Join(e.p.root, "untouched")
			mustWrite(t, outside, []byte("PRIVATE OUTSIDE"))
			switch mode {
			case "symlink":
				os.Remove(path)
				os.Symlink(outside, path)
			case "hardlink":
				os.Remove(path)
				os.Link(outside, path)
			case "fifo":
				os.Remove(path)
				syscall.Mkfifo(path, 0600)
			case "directory":
				os.Remove(path)
				os.Mkdir(path, 0700)
			case "oversize":
				mustWrite(t, path, bytes.Repeat([]byte("a"), filesMaxBytes+1))
			case "permissions":
				os.Chmod(path, 0644)
			case "sealed-link":
				os.Symlink(outside, path+".sealed")
			case "store-link":
				dir := filepath.Dir(path)
				os.Rename(dir, dir+"-real")
				os.Symlink(dir+"-real", dir)
			case "lock-link":
				os.Remove(filesPath(e, "files.lock"))
				os.Symlink(outside, filesPath(e, "files.lock"))
			}
			if err := filesCall(t, e, "", nil, "inspect", id); err == nil {
				t.Fatal("unsafe session inspected")
			}
			if err := filesCall(t, e, base64.StdEncoding.EncodeToString([]byte("x")), nil, "append", id, "8"); err == nil {
				t.Fatal("unsafe session appended")
			}
			if read(t, outside) != "PRIVATE OUTSIDE" {
				t.Fatal("external file changed")
			}
			if strings.Contains(e.out.(*bytes.Buffer).String(), "PRIVATE OUTSIDE") {
				t.Fatal("unsafe data exposed")
			}
		})
	}
}

func TestFilesArguments(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, args := range [][]string{
		nil, {"/module"}, {"relative", "create"}, {"/module", "backups", "--root", "/tmp"},
		{"/module", "create", "--root", "/tmp"}, {"/module", "append", id, "-1"},
		{"/module", "append", id, "1048577"}, {"/module", "append", id, "0", "PRIVATE"},
		{"/module", "append", "../outside", "0"}, {"/module", "discard", "/outside"},
		{"/module", "inspect", id + "/../x"}, {"/module", "install", id},
		{"/module", "restore", "../../keybox.xml.bak.1", id},
		{"/module", "install", id, "bad"}, {"/module", "unknown"},
	} {
		if code := Files(args); code != 2 {
			t.Fatalf("accepted invalid args %v: %d", args, code)
		}
	}
}

func TestFilesConcurrentAppendAndInstallLock(t *testing.T) {
	e := fixture(t)
	id, _ := filesNew(t, e, "", false)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		other := production(e.p.module)
		other.p = e.p
		other.out = io.Discard
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- other.files(context.Background(), []string{"append", id, "0"}, strings.NewReader("eA=="))
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 || read(t, filesPath(e, id)) != "x" {
		t.Fatal("append offset check was not atomic")
	}
	_, p := filesNew(t, e, syntheticKeybox(t), true)
	action, err := lock(context.Background(), filepath.Join(e.p.state, "action.lock"), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer action.Close()
	called := false
	e.d.verify = func(context.Context, string, string, io.Writer) int { called = true; return 0 }
	if err := e.filesInstall(context.Background(), p.ID, p.Revision, false); err == nil || called {
		t.Fatal("manual install bypassed action.lock")
	}
	unchanged(t, e)
}

func TestFilesEmptyBackupsAndNoBackend(t *testing.T) {
	e := fixture(t)
	result := filesBackupsCall(t, e)
	if len(result.Backups) != 0 || result.Target == "" {
		t.Fatal("empty list contract")
	}
	id, _ := filesNew(t, e, "invalid", false)
	os.RemoveAll(e.host("/data/adb/modules"))
	if err := filesCall(t, e, "", nil, "inspect", id); err == nil {
		t.Fatal("inspection without active backend accepted")
	}
	if err := filesCall(t, e, "", nil, "backups"); err == nil {
		t.Fatal("listing without active backend accepted")
	}
	// Even a guessed revision cannot install without an active backend.
	if err := e.filesInstall(context.Background(), id, strings.Repeat("a", 64), false); err == nil {
		t.Fatal("install without active backend accepted")
	}
}

func TestFilesInvalidPreviewJSONDoesNotExposeXML(t *testing.T) {
	e := fixture(t)
	for _, input := range []string{
		"<AndroidAttestation><PRIVATESECRET>",
		`<AndroidAttestation><Keybox><Key algorithm="PRIVATESECRET"/></Keybox></AndroidAttestation>`,
		`<?xml version="PRIVATESECRET"?><AndroidAttestation/>`,
	} {
		_, p := filesNew(t, e, input, true)
		raw, err := json.Marshal(p)
		if err != nil || p.Error == "" || bytes.Contains(raw, []byte("PRIVATESECRET")) {
			t.Fatalf("unsafe error serialization: %s: %v", raw, err)
		}
	}
}

func TestFilesPreviewBackendCompatibility(t *testing.T) {
	e := fixture(t)
	data := syntheticKeybox(t)
	// A complete XML document can still fail the selected backend's constraints.
	data = strings.ReplaceAll(data, "<NumberOfCertificates>2</NumberOfCertificates>", "<NumberOfCertificates>1</NumberOfCertificates>")
	tgt, _ := e.target()
	tgt.Backend = backend.OhMyKeymint
	e.d.detect = func(string) (backend.Report, error) { return backend.Report{Selected: &tgt}, nil }
	_, p := filesNew(t, e, data, true)
	if !p.StructureValid || p.Compatible || p.Validity != nil || p.Integrity != nil || len(p.Serials) != 0 {
		t.Fatal("backend constraints did not stop certificate extraction")
	}
}

func TestFilesPreviewValidityFailure(t *testing.T) {
	e := fixture(t)
	_, p := filesNew(t, e, "<AndroidAttestation><Certificate>invalid</Certificate></AndroidAttestation>", true)
	if p.Validity != nil || p.Integrity != nil || len(p.Serials) != 0 {
		t.Fatalf("invalid certificates reached extraction: %+v", p)
	}
	if p.Error == "" || p.Compatible || !p.StructureValid {
		t.Fatal("bad certificate reported compatible")
	}
}

func TestFilesRestrictiveUmaskAndCancelledStoreLock(t *testing.T) {
	e := fixture(t)
	// Only descriptor creation is under this process-wide umask; no goroutines
	// or subprocesses run until it is restored.
	s, err := e.filesStore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	previous := syscall.Umask(0777)
	f, createErr := filesOpen(s.dir, strings.Repeat("d", 64), syscall.O_CREAT|syscall.O_EXCL|syscall.O_WRONLY, true)
	syscall.Umask(previous)
	if createErr != nil {
		s.close()
		t.Fatal(createErr)
	}
	st, statErr := f.Stat()
	f.Close()
	if statErr != nil || st.Mode().Perm() != 0600 {
		s.close()
		t.Fatal("new private file mode follows umask")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if other, err := e.filesStore(ctx); err == nil {
		other.close()
		s.close()
		t.Fatal("cancelled storage operation acquired lock")
	}
	s.close()
}

func TestFilesVerifyFailureMessagesSafe(t *testing.T) {
	e := fixture(t)
	id, p := filesNew(t, e, syntheticKeybox(t), true)
	e.d.prepare = func(context.Context, string, []byte, string) ([]byte, error) {
		return nil, fmt.Errorf("PRIVATE SECRET XML")
	}
	err := e.filesInstall(context.Background(), id, p.Revision, false)
	if err == nil || strings.Contains(err.Error(), "PRIVATE SECRET") {
		t.Fatal("preparation diagnostics exposed input")
	}
	unchanged(t, e)
	// JSON types exactly match the existing installed-info report.
	var _ *keybox.Validity = p.Validity
	var _ *keybox.Integrity = p.Integrity
}
