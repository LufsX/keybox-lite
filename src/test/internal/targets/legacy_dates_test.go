package targets

import (
	"strings"
	"testing"
)

func TestLegacyAndroidPatchDates(t *testing.T) {
	for _, raw := range []string{
		"system=2026-10-05\nvendor=2026-10-05\nboot=2026-10-05\n",
		"all=2026-10-05\n",
		"2026-10-05\n",
	} {
		t.Run(strings.TrimSpace(raw), func(t *testing.T) {
			m, root := fixture(t, true)
			put(t, root, targetPath, "com.a\n")
			put(t, root, patchPath, raw+"future=keep\n")
			s := getStatus(t, m)
			if s.ConfigError != "" {
				t.Fatal(s.ConfigError)
			}
			for key, want := range (Policy{"os_patch": "202610", "vendor_patch": "20261005", "boot_patch": "20261005"}) {
				if got := s.Config.DefaultPolicy[key]; got != want {
					t.Fatalf("%s=%q want %q", key, got, want)
				}
			}
			r := request(s)
			r.Targets = append(r.Targets, Target{"com.new", "generate"})
			if err := m.save(r); err != nil {
				t.Fatal(err)
			}
			saved := read(t, root, patchPath)
			for _, want := range []string{"system=2026-10-05", "vendor=2026-10-05", "boot=2026-10-05", "future=keep"} {
				if !strings.Contains(saved, want) {
					t.Fatalf("missing %q in %q", want, saved)
				}
			}
			s = getStatus(t, m)
			if s.ConfigError != "" || len(s.Config.Targets) != 2 {
				t.Fatalf("%+v", s)
			}
			r = request(s)
			r.DefaultPolicy["boot_patch"] = "20261101"
			if err := m.save(r); err != nil {
				t.Fatal(err)
			}
			if saved = read(t, root, patchPath); !strings.Contains(saved, "boot=20261101") || !strings.Contains(saved, "vendor=2026-10-05") {
				t.Fatal(saved)
			}
		})
	}
}

func TestLegacyInvalidAndroidPatchDatesPreserveFiles(t *testing.T) {
	for _, value := range []string{"2026-02-30", "2026-13-05", "2026-1-05", "2026-10-05x", "20260230"} {
		for _, key := range []string{"all", "system", "vendor", "boot"} {
			m, root := fixture(t, true)
			original := key + "=" + value + "\n"
			put(t, root, targetPath, "com.a\n")
			put(t, root, patchPath, original)
			s := getStatus(t, m)
			if s.ConfigError == "" {
				t.Fatalf("accepted %s=%s", key, value)
			}
			if err := m.save(request(s)); err == nil {
				t.Fatalf("saved invalid %s=%s", key, value)
			}
			if read(t, root, patchPath) != original || read(t, root, targetPath) != "com.a\n" {
				t.Fatal("invalid configuration overwritten")
			}
		}
	}
}
