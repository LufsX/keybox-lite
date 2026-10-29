package verify

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitEmptyAccelerationDoesNotEnableDefaultProxy(t *testing.T) {
	dir := t.TempDir()
	keybox := testKeybox(t, dir)
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\nfor arg do url=$arg; done\ncase \"$url\" in\nhttps://android.googleapis.com/attestation/status) exit 22;;\n*) printf '%s' '{\"entries\":{}}';;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("TMPDIR", dir)
	t.Setenv("KB_ACCEL", "")
	if rc := Run([]string{keybox}); rc != 2 {
		t.Fatalf("empty KB_ACCEL used default proxy: Run = %d, want stale/unknown 2", rc)
	}
}
