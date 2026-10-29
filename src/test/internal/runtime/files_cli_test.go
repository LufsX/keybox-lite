package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"keyboxlite/internal/keybox"
)

// The command adapter runs in separate processes, backed only by a temporary
// sysroot and explicit prepare/verify dependencies. Production has no test root.
func TestFilesIsolatedProcess(t *testing.T) {
	root := os.Getenv("KBL_FILES_PROCESS_ROOT")
	if root == "" {
		return
	}
	var args []string
	for i, arg := range os.Args {
		if arg == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	e := production(filepath.Join(root, "module"))
	e.p = paths{root: root, module: filepath.Join(root, "module"), state: filepath.Join(root, "data/adb/keybox-lite"), conf: filepath.Join(root, "data/adb/keybox-lite/config"), tmp: filepath.Join(root, "tmp")}
	e.d.prepare = func(_ context.Context, kind string, raw []byte, _ string) ([]byte, error) {
		return keybox.Prepare(raw, kind)
	}
	e.d.verify = func(context.Context, string, string, io.Writer) int { return 0 }
	if err := e.files(context.Background(), args, os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func filesSubprocess(t *testing.T, root, input string, args ...string) ([]byte, error) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, append([]string{"-test.run=^TestFilesIsolatedProcess$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "KBL_FILES_PROCESS_ROOT="+root)
	cmd.Stdin = strings.NewReader(input)
	return cmd.CombinedOutput()
}

func TestFilesTEESCustomBackupNameInspectRestore(t *testing.T) {
	data := syntheticKeybox(t)
	for _, name := range []string{
		"my.bak.keybox.xml",
		"my.bak.keybox.xml.bak.123",
		" custom key.xml ",
		"密钥.xml",
		".hidden.xml",
		"-key'box;$().xml",
	} {
		t.Run(name, func(t *testing.T) {
			e := fixture(t)
			if err := os.RemoveAll(e.host("/data/adb/modules")); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, e.host("/data/adb/modules/teesim/module.prop"), []byte("versionCode=1\n"))
			config, err := json.Marshal(map[string]any{"profiles": map[string]any{"default": map[string]string{"keybox": "nested/" + name}}})
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, e.host("/data/adb/teesim/config.json"), config)
			target, err := e.target()
			if err != nil || target.KeyboxPath != "/data/adb/teesim/nested/"+name {
				t.Fatalf("configured target name rejected: %+v %v", target, err)
			}
			path := e.host(target.KeyboxPath)
			mustWrite(t, path, []byte("OLD"))
			id := name + ".bak.123456"
			mustWrite(t, path+".bak.123456", []byte(data))
			run := func(result any, args ...string) []byte {
				t.Helper()
				raw, err := filesSubprocess(t, e.p.root, "", args...)
				if err != nil {
					t.Fatalf("%s: %v: %s", args[0], err, raw)
				}
				if result != nil {
					if err := json.Unmarshal(raw, result); err != nil {
						t.Fatalf("%s JSON: %v: %s", args[0], err, raw)
					}
				}
				return raw
			}
			var listed filesBackups
			run(&listed, "backups")
			if len(listed.Backups) != 1 || listed.Backups[0].ID != id {
				t.Fatalf("custom backup not listed: %+v", listed)
			}
			var preview Preview
			run(&preview, "inspect", id)
			if preview.Revision != listed.Backups[0].Revision || !preview.Compatible || preview.Target != target.KeyboxPath {
				t.Fatalf("custom backup inspection mismatch: %+v", preview)
			}
			// Generic suffix validation must not weaken the exact target binding.
			otherID := "other.bak.keybox.xml.bak.123456"
			mustWrite(t, filepath.Join(filepath.Dir(path), otherID), []byte(data))
			for _, args := range [][]string{{"inspect", otherID}, {"restore", otherID, preview.Revision}} {
				if _, err := filesSubprocess(t, e.p.root, "", args...); err == nil {
					t.Fatalf("other target's backup accepted: %v", args)
				}
			}
			raw := run(nil, "restore", id, preview.Revision)
			if !bytes.Contains(raw, []byte("[DONE]")) || read(t, path) != data {
				t.Fatal("custom backup restore failed")
			}
			oldFound := false
			for _, p := range filesBackupsCall(t, e).Backups {
				if read(t, filepath.Join(filepath.Dir(path), p.ID)) == "OLD" {
					oldFound = true
				}
			}
			if !oldFound {
				t.Fatal("custom target's previous keybox was not backed up")
			}
			clean(t, e)
		})
	}
}

func TestFilesBackupNameUsesLastNumericSuffix(t *testing.T) {
	for _, id := range []string{"my.bak.keybox.xml.bak.123", ".密钥 key.xml.bak.0", "my.bak.123.bak.456"} {
		if !filesBackupName(id) {
			t.Fatalf("valid backup suffix rejected: %s", id)
		}
	}
	for _, id := range []string{".bak.123", "my.bak.keybox.xml.bak.", "my.bak.keybox.xml.bak.no", "my.bak.123.bak.45x", "../my.bak.keybox.xml.bak.123"} {
		if filesBackupName(id) {
			t.Fatalf("invalid backup suffix accepted: %s", id)
		}
	}
}

func TestFilesChunkedCLIInstallRestore(t *testing.T) {
	e := fixture(t)
	run := func(input string, result any, args ...string) []byte {
		t.Helper()
		raw, err := filesSubprocess(t, e.p.root, input, args...)
		if err != nil {
			t.Fatalf("%s: %v: %s", args[0], err, raw)
		}
		if result != nil {
			if err = json.Unmarshal(raw, result); err != nil {
				t.Fatalf("%s JSON: %v: %s", args[0], err, raw)
			}
		}
		return raw
	}
	var created struct{ ID string }
	run("", &created, "create")
	// Padding forces several frontend-sized chunks without passing any XML in argv.
	data := syntheticKeybox(t)
	data = strings.Replace(data, "<AndroidAttestation>", "<AndroidAttestation><!--"+strings.Repeat("padding", 9000)+"-->", 1)
	for offset := 0; offset < len(data); {
		end := offset + filesChunkBytes
		if end > len(data) {
			end = len(data)
		}
		var reply struct {
			OK   bool
			Size int64
		}
		run(base64.StdEncoding.EncodeToString([]byte(data[offset:end])), &reply, "append", created.ID, fmt.Sprint(offset))
		if !reply.OK || reply.Size != int64(end) {
			t.Fatal("chunk acknowledgement incorrect")
		}
		offset = end
	}
	var p Preview
	run("", &p, "inspect", created.ID)
	if !p.Compatible || p.Revocation != "not_checked" || p.Revision == "" {
		t.Fatal("invalid inspection")
	}
	if raw, err := filesSubprocess(t, e.p.root, "eA==", "append", created.ID, fmt.Sprint(len(data))); err == nil || bytes.Contains(raw, []byte("PRIVATE KEY")) {
		t.Fatal("sealed CLI session accepted append or exposed XML")
	}
	raw := run("", nil, "install", created.ID, p.Revision)
	if !bytes.Contains(raw, []byte("[DONE]")) || bytes.Contains(raw, []byte("PRIVATE KEY")) || json.Valid(raw) {
		t.Fatalf("install must print safe progress: %s", raw)
	}
	var backups filesBackups
	run("", &backups, "backups")
	if len(backups.Backups) != 1 || backups.Backups[0].ID == "" {
		t.Fatal("old target backup missing")
	}
	// The old target from fixture is intentionally invalid and cannot be restored.
	if _, err := filesSubprocess(t, e.p.root, "", "restore", backups.Backups[0].ID, backups.Backups[0].Revision); err == nil {
		t.Fatal("invalid backup restored")
	}
	target := e.host("/data/adb/tricky_store/keybox.xml")
	current := read(t, target)
	mustWrite(t, target+".bak.123456", []byte(current))
	run("", &backups, "backups")
	var restore Preview
	for _, entry := range backups.Backups {
		if entry.ID == "keybox.xml.bak.123456" {
			restore = entry
		}
	}
	var inspected Preview
	run("", &inspected, "inspect", restore.ID)
	if inspected.Revision != restore.Revision || !inspected.Compatible {
		t.Fatal("backup inspection changed revision")
	}
	raw = run("", nil, "restore", inspected.ID, inspected.Revision)
	if !bytes.Contains(raw, []byte("[DONE]")) || read(t, target) != current {
		t.Fatal("CLI restore failed")
	}
	var reply struct{ OK bool }
	run("", &reply, "discard", created.ID)
	if !reply.OK {
		t.Fatal("discard acknowledgement incorrect")
	}
	clean(t, e)
}
