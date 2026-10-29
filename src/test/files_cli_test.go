package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Valid operations are exercised with private engine dependencies in runtime.
// This subprocess checks the production dispatcher only with invalid arguments;
// every case must fail argument validation before Android storage is touched.
func TestFilesCLIProcess(t *testing.T) {
	if os.Getenv("KBL_FILES_CLI_HELPER") != "1" {
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

func TestFilesCLIDispatchAndRootOverrideRejection(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	module := filepath.Join(root, "module")
	id := strings.Repeat("a", 64)
	for _, args := range [][]string{
		{"keybox-files"}, {"keybox-files", module}, {"keybox-files", "relative", "create"},
		{"keybox-files", module, "backups", "--root", root},
		{"keybox-files", module, "create", "--root", root},
		{"keybox-files", module, "append", id, "0", "--root", root},
		{"keybox-files", module, "inspect", "../outside"},
		{"keybox-files", module, "discard", "../outside"},
		{"keybox-files", module, "install", id, id, "--root", root},
		{"keybox-files", module, "restore", "keybox.xml.bak.123", id, "--root", root},
		{"keybox-files", module, "append", id, "-1"},
		{"keybox-files", module, "append", id, "1048577"},
		{"keybox-files", module, "append", id, "0", "PRIVATE XML"},
		{"keybox-files", module, "unknown"},
	} {
		cmd := exec.Command(exe, append([]string{"-test.run=^TestFilesCLIProcess$", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "KBL_FILES_CLI_HELPER=1")
		raw, err := cmd.CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 || !strings.Contains(string(raw), "usage: keybox-files") {
			t.Fatalf("dispatch %v: %s: %v", args, raw, err)
		}
		if strings.Contains(string(raw), "PRIVATE XML") {
			t.Fatal("usage echoed private input")
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("invalid CLI wrote to filesystem")
	}
}
