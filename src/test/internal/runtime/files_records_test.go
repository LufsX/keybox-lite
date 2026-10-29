package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesSuccessRecordsHistoryAndSafeLog(t *testing.T) {
	e := fixture(t)
	history := filepath.Join(e.p.tmp, "keybox-update.history")
	logPath := filepath.Join(e.p.tmp, "keybox-update.log")
	old := "2026-01-01 00:00:00 | meow | 123 bytes | installed\n"
	mustWrite(t, history, []byte(old))
	id, p := filesNew(t, e, syntheticKeybox(t), true)
	e.d.verify = func(_ context.Context, _, _ string, out io.Writer) int {
		fmt.Fprintln(out, "PRIVATEKEY-DIAGNOSTIC")
		return 0
	}
	if err := e.filesInstall(context.Background(), id, p.Revision, false); err != nil {
		t.Fatal(err)
	}
	got := read(t, history)
	if !strings.HasPrefix(got, old) || !strings.Contains(got, " | local-import | ") || strings.Count(got, "| installed") != 2 {
		t.Fatalf("manual success history not appended: %s", got)
	}
	log := read(t, logPath)
	if !strings.Contains(log, "[START] Keybox replacement: local-import") || !strings.Contains(log, "[DONE]") ||
		strings.Contains(log, "PRIVATEKEY-DIAGNOSTIC") || strings.Contains(log, "PRIVATE KEY") || strings.Contains(log, "<AndroidAttestation") {
		t.Fatalf("unsafe/incomplete persisted log: %s", log)
	}
	for _, path := range []string{history, logPath} {
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != 0600 {
			t.Fatalf("record mode not private: %s", path)
		}
	}
	target := e.host("/data/adb/tricky_store/keybox.xml")
	mustWrite(t, target+".bak.123", []byte(read(t, target)))
	var preview Preview
	if err := filesCall(t, e, "", &preview, "inspect", "keybox.xml.bak.123"); err != nil {
		t.Fatal(err)
	}
	if err := e.filesInstall(context.Background(), preview.ID, preview.Revision, true); err != nil {
		t.Fatal(err)
	}
	got = read(t, history)
	if !strings.Contains(got, " | backup-restore | ") || strings.Count(got, "| installed") != 3 {
		t.Fatal("restore success missing from history")
	}
	if !strings.Contains(read(t, logPath), "backup-restore") {
		t.Fatal("restore log source incorrect")
	}
	for _, pattern := range []string{logPath + ".*", history + ".*"} {
		matches, _ := filepath.Glob(pattern)
		if len(matches) != 0 {
			t.Fatalf("temporary record leaked: %v", matches)
		}
	}
}

func TestFilesRecordFailuresDoNotFailInstalledKeybox(t *testing.T) {
	data := syntheticKeybox(t)
	for _, mode := range []string{"log-directory", "history-directory", "history-symlink", "history-hardlink", "history-large", "history-rename"} {
		t.Run(mode, func(t *testing.T) {
			e := fixture(t)
			history := filepath.Join(e.p.tmp, "keybox-update.history")
			logPath := filepath.Join(e.p.tmp, "keybox-update.log")
			outside := filepath.Join(e.p.root, "outside")
			mustWrite(t, outside, []byte("UNTOUCHED"))
			switch mode {
			case "log-directory":
				os.Mkdir(logPath, 0700)
			case "history-directory":
				os.Mkdir(history, 0700)
			case "history-symlink":
				os.Symlink(outside, history)
			case "history-hardlink":
				os.Link(outside, history)
			case "history-large":
				mustWrite(t, history, bytes.Repeat([]byte("x"), filesMaxBytes+1))
			case "history-rename":
				e.d.rename = func(from, to string) error {
					if to == history {
						return fmt.Errorf("history cannot be persisted")
					}
					return os.Rename(from, to)
				}
			}
			id, p := filesNew(t, e, data, true)
			e.out = &bytes.Buffer{}
			if err := e.filesInstall(context.Background(), id, p.Revision, false); err != nil {
				t.Fatalf("record failure changed install result: %v", err)
			}
			if read(t, e.host("/data/adb/tricky_store/keybox.xml")) != data {
				t.Fatal("record failure blocked installation")
			}
			if read(t, outside) != "UNTOUCHED" {
				t.Fatal("record followed unsafe link")
			}
			if output := e.out.(*bytes.Buffer).String(); !strings.Contains(output, "[WARN]") || !strings.Contains(output, "[DONE]") || strings.Contains(output, "[FAIL]") {
				t.Fatal("record failure reported install failure")
			}
			for _, pattern := range []string{logPath + ".*", history + ".*"} {
				matches, _ := filepath.Glob(pattern)
				if len(matches) != 0 {
					t.Fatalf("failed record left staging: %v", matches)
				}
			}
		})
	}
}

func TestFilesPersistentLogDoesNotFollowPublicLinks(t *testing.T) {
	for _, hardlink := range []bool{false, true} {
		t.Run(fmt.Sprint(hardlink), func(t *testing.T) {
			e := fixture(t)
			public := filepath.Join(e.p.tmp, "keybox-update.log")
			outside := filepath.Join(e.p.root, "outside")
			mustWrite(t, outside, []byte("UNTOUCHED"))
			if hardlink {
				os.Link(outside, public)
			} else {
				os.Symlink(outside, public)
			}
			id, p := filesNew(t, e, syntheticKeybox(t), true)
			if err := e.filesInstall(context.Background(), id, p.Revision, false); err != nil {
				t.Fatal(err)
			}
			st, _ := os.Lstat(public)
			if !filesRegular(st) || read(t, outside) != "UNTOUCHED" {
				t.Fatal("log publication followed an existing link")
			}
		})
	}
}

func TestFilesFailureLogNeverAppendsSuccess(t *testing.T) {
	e := fixture(t)
	history := filepath.Join(e.p.tmp, "keybox-update.history")
	old := "previous safe history\n"
	mustWrite(t, history, []byte(old))
	id, p := filesNew(t, e, syntheticKeybox(t), true)
	e.d.verify = func(_ context.Context, _, _ string, out io.Writer) int {
		fmt.Fprintln(out, "PRIVATE XML")
		return 2
	}
	if err := e.filesInstall(context.Background(), id, p.Revision, false); err == nil {
		t.Fatal("verification failure installed")
	}
	if read(t, history) != old {
		t.Fatal("failed install appended success history")
	}
	log := read(t, filepath.Join(e.p.tmp, "keybox-update.log"))
	if !strings.Contains(log, "[FAIL] strict verification failed (exit 2)") || strings.Contains(log, "PRIVATE XML") {
		t.Fatal("failure log missing safe diagnostic")
	}
}

func TestFilesInvalidCertificateArraysAreConsistent(t *testing.T) {
	e := fixture(t)
	_, p := filesNew(t, e, "<AndroidAttestation><Certificate>bad</Certificate></AndroidAttestation>", true)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"serials":[]`, `"validity":null`, `"integrity":null`, `"revocation":"not_checked"`} {
		if !bytes.Contains(raw, []byte(expected)) {
			t.Fatalf("missing consistent JSON field %s: %s", expected, raw)
		}
	}
	_, malformed := filesNew(t, e, "not XML", true)
	raw, _ = json.Marshal(malformed)
	for _, expected := range []string{`"serials":[]`, `"validity":null`, `"integrity":null`} {
		if !bytes.Contains(raw, []byte(expected)) {
			t.Fatalf("malformed XML fields inconsistent: %s", raw)
		}
	}
}
