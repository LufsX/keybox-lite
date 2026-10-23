package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandaloneShellEntrypoints(t *testing.T) {
	shells := []string{"sh"}
	if path, err := exec.LookPath("mksh"); err == nil {
		shells = append(shells, path)
	}
	for _, shell := range shells {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			root := t.TempDir()
			module := filepath.Join(root, "module with spaces")
			os.MkdirAll(filepath.Join(module, "bin"), 0700)
			captured := filepath.Join(root, "args")
			binary := filepath.Join(module, "bin/keyboxlite")
			mustWrite(t, binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$KBL_CAPTURE\"\n"))
			os.Chmod(binary, 0700)
			for _, name := range []string{"action.sh", "settings.sh", "service.sh", "uninstall.sh"} {
				raw, err := os.ReadFile(filepath.Join("..", "..", "..", name))
				if err != nil {
					t.Fatal(err)
				}
				mustWrite(t, filepath.Join(module, name), raw)
			}
			for _, tc := range []struct {
				script     string
				args, want []string
			}{
				{"action.sh", nil, []string{"run-update", filepath.Join(module, "action.sh"), "/data/adb/keybox-lite"}},
				{"action.sh", []string{"--managed"}, []string{"update", module}},
				{"settings.sh", []string{"get"}, []string{"settings", module, "get"}},
				{"settings.sh", []string{"set", "weekly"}, []string{"settings", module, "set", "weekly"}},
				{"service.sh", nil, []string{"service", module}},
				{"service.sh", []string{"--worker"}, []string{"service", module, "--worker"}},
				{"uninstall.sh", nil, []string{"uninstall", module}},
			} {
				cmd := exec.Command(shell, append([]string{filepath.Join(module, tc.script)}, tc.args...)...)
				cmd.Env = append(os.Environ(), "KBL_CAPTURE="+captured)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: %v %s", tc.script, err, output)
				}
				got := strings.Split(strings.TrimSuffix(read(t, captured), "\n"), "\n")
				if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
					t.Fatalf("%s: args %q, want %q", tc.script, got, tc.want)
				}
			}
		})
	}
}
