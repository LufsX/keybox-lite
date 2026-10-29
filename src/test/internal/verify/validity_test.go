package verify

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunRejectsTimeInvalidBeforeDownloading(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name          string
		before, after time.Time
	}{
		{"expired", now.Add(-2 * time.Hour), now.Add(-time.Hour)},
		{"not yet valid", now.Add(time.Hour), now.Add(2 * time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := testKeybox(t, dir, tc.before, tc.after)
			log := filepath.Join(dir, "network-called")
			stub := "#!/bin/sh\nprintf called > '" + log + "'\nexit 1\n"
			if err := os.WriteFile(filepath.Join(dir, "curl"), []byte(stub), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			// A valid cert would fail creating its workdir with code 2.
			t.Setenv("TMPDIR", filepath.Join(dir, "missing"))
			if got := Run([]string{path}); got != 3 {
				t.Fatalf("got %d, want time-invalid (3)", got)
			}
			if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatal("download attempted")
			}
		})
	}
}
