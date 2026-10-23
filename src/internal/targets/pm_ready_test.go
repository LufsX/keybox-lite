package targets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPMEmptySystemListUnavailableAndEmptyUserListAllowed(t *testing.T) {
	root := t.TempDir()
	put(t, root, "/bin/placeholder", "")
	script := filepath.Join(root, "bin/pm")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(script, []byte("#!/bin/sh\n"+s), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", filepath.Dir(script)+":"+os.Getenv("PATH"))
	for _, s := range []string{"exit 0\n", "case \"$*\" in *-s*) exit 0;; *) printf 'package:com.user\\n';; esac\n"} {
		write(s)
		if _, err := listPackages(root); err == nil || !strings.Contains(err.Error(), "packages unavailable") {
			t.Fatalf("empty system pm result accepted: %v", err)
		}
	}
	write("case \"$*\" in *-s*) printf 'package:android\\n';; *) exit 0;; esac\n")
	packages, err := listPackages(root)
	if err != nil || len(packages) != 1 || !packages["android"] || len(userNames(packages)) != 0 {
		t.Fatalf("empty user list rejected: %v %v", packages, err)
	}
}
func TestAutoEmptyPMOutputPreservesBaselineUntilReady(t *testing.T) {
	m, root := fixture(t, false)
	script := filepath.Join(root, "tools/pm")
	if err := os.MkdirAll(filepath.Dir(script), 0755); err != nil {
		t.Fatal(err)
	}
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(script, []byte("#!/bin/sh\n"+s), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", filepath.Dir(script)+":"+os.Getenv("PATH"))
	m.list = func() (map[string]bool, error) { return listPackages(root) }
	write("case \"$*\" in *-s*) printf 'package:android\\n';; *) printf 'package:com.old\\n';; esac\n")
	r := request(getStatus(t, m))
	r.AutoAddNewApps = true
	if err := m.save(r); err != nil {
		t.Fatal(err)
	}
	baseline := read(t, root, statePath)
	config := read(t, root, configPath)
	write("exit 0\n")
	if err := m.auto(); err == nil || !strings.Contains(err.Error(), "packages unavailable") {
		t.Fatalf("auto should retry empty pm: %v", err)
	}
	if read(t, root, statePath) != baseline || read(t, root, configPath) != config {
		t.Fatal("empty pm reset baseline or config")
	}
	if s := getStatus(t, m); !strings.Contains(s.PackagesError, "packages unavailable") {
		t.Fatalf("empty pm error hidden: %+v", s)
	}
	write("case \"$*\" in *-s*) printf 'package:android\\n';; *) printf 'package:com.old\\npackage:com.new\\n';; esac\n")
	if err := m.auto(); err != nil {
		t.Fatal(err)
	}
	s := getStatus(t, m)
	if len(s.Config.Targets) != 1 || s.Config.Targets[0].PackageName != "com.new" {
		t.Fatalf("pre-existing apps were mass-added after retry: %+v", s.Config.Targets)
	}
}
