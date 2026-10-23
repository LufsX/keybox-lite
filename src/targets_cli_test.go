package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This subprocess uses the actual CLI dispatcher with a private filesystem root.
func TestTargetsCLIProcess(t *testing.T) {
	if os.Getenv("KBL_CLI_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"keyboxlite"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	os.Exit(99)
}

func targetsCLI(t *testing.T, root, stdin string, args ...string) ([]byte, error) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := append([]string{"-test.run=^TestTargetsCLIProcess$", "--", "targets", "--root", root, "--module-dir", "/data/adb/modules/keybox-lite"}, args...)
	cmd := exec.Command(exe, command...)
	cmd.Env = append(os.Environ(), "KBL_CLI_HELPER=1")
	cmd.Stdin = strings.NewReader(stdin)
	return cmd.CombinedOutput()
}

func TestTargetsCLIReadSaveConflictAndInjectedEntry(t *testing.T) {
	root := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("data/adb/modules/keybox-lite/module.prop", "id=keybox-lite\n")
	write("data/adb/modules/keybox-lite/webroot/index.html", "<html></html>")
	write("data/adb/modules/keybox-lite/webui.sh", "#!/system/bin/sh\n")
	write("data/adb/modules/tricky_store/module.prop", "id=tricky_store\nversionCode=246\n")
	configPath := filepath.Join(root, "data/adb/tricky_store/config.ini")
	write("data/adb/tricky_store/config.ini", "[default_policy]\nos_patch = prop\nfuture_key = keep\n\n[target]\ncom.example.old\n\n[future_feature]\nflag = keep\n")
	// Package manager responses come only from this local stub.
	bin := filepath.Join(root, "tools")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "pm"), []byte("#!/bin/sh\ncase \"$*\" in *-s*) printf 'package:com.android.settings\\n';; *) printf 'package:com.example.old\\npackage:com.example.new\\n';; esac\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	raw, err := targetsCLI(t, root, "", "status")
	if err != nil {
		t.Fatalf("status: %v: %s", err, raw)
	}
	var status struct {
		Supported bool   `json:"supported"`
		Revision  string `json:"revision"`
		Config    struct {
			Targets []struct{ PackageName, Mode string } `json:"targets"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	if !status.Supported || status.Revision == "" {
		t.Fatalf("invalid status %s", raw)
	}
	request, _ := json.Marshal(map[string]any{
		"revision":       status.Revision,
		"targets":        []map[string]string{{"package_name": "com.example.new", "mode": "generate"}, {"package_name": "uid:10123", "mode": "hack"}},
		"default_policy": map[string]string{"os_patch": "202610"},
		"per_app_policy": map[string]map[string]string{"com.example.new": {"boot_patch": "20261001"}},
		"system_apps":    []string{}, "auto_add_new_apps": false,
	})
	raw, err = targetsCLI(t, root, string(request), "save")
	if err != nil || !bytes.Contains(raw, []byte(`"ok":true`)) {
		t.Fatalf("save: %v: %s", err, raw)
	}
	written, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"com.example.new!", "uid:10123?", "202610", "20261001", "future_key = keep", "[future_feature]", "flag = keep"} {
		if !strings.Contains(string(written), want) {
			t.Fatalf("missing %q: %s", want, written)
		}
	}
	if raw, err = targetsCLI(t, root, string(request), "save"); err == nil {
		t.Fatalf("stale save accepted: %s", raw)
	}
	if after, err := os.ReadFile(configPath); err != nil || !bytes.Equal(after, written) {
		t.Fatal("stale save changed config")
	}
	raw, err = targetsCLI(t, root, "", "entry", "set", "on")
	if err != nil || !bytes.Contains(raw, []byte(`"ok":true`)) {
		t.Fatalf("entry on: %v: %s", err, raw)
	}
	link := filepath.Join(root, "data/adb/modules/tricky_store/webroot")
	if _, err := os.Readlink(link); err != nil {
		t.Fatalf("entry link: %v", err)
	}
	raw, err = targetsCLI(t, root, "", "entry", "set", "off")
	if err != nil {
		t.Fatalf("entry off: %v: %s", err, raw)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("entry remains: %v", err)
	}
}
