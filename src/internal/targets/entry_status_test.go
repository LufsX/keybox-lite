package targets

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEntryStatusAndOffIncludeOrphanedLinks(t *testing.T) {
	for _, id := range backendIDs {
		for _, name := range []string{"webroot", "action.sh"} {
			t.Run(id+"/"+name, func(t *testing.T) {
				m, root := fixture(t, false)
				p := "/data/adb/modules/" + id + "/" + name
				symlink(t, root, p, m.linkSource(name))
				// State is still off: the real link must remain visible and removable.
				for _, command := range [][]string{{"status"}, {"entry", "status"}} {
					var out, errout bytes.Buffer
					args := append([]string{"--root", root, "--module-dir", module}, command...)
					if code := run(args, strings.NewReader(""), &out, &errout); code != 0 {
						t.Fatalf("status failed: %s", errout.String())
					}
					var entry Entry
					if len(command) == 1 {
						var status Status
						if err := json.Unmarshal(out.Bytes(), &status); err != nil {
							t.Fatal(err)
						}
						entry = status.Entry
					} else if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
						t.Fatal(err)
					}
					if entry.Enabled || !entry.Linked || entry.HasOwnWebUI {
						t.Fatalf("hidden orphan: %+v", entry)
					}
				}
				entry, err := m.entryStatus(defaultState(), nil)
				if err != nil || !entry.Linked {
					t.Fatalf("missing backend hid link: %+v %v", entry, err)
				}
				var out, errout bytes.Buffer
				if code := run([]string{"--root", root, "--module-dir", module, "entry", "set", "off"}, strings.NewReader(""), &out, &errout); code != 0 {
					t.Fatalf("off failed: %s", errout.String())
				}
				absent(t, root, p)
				if s := getStatus(t, m); s.Entry.Enabled || s.Entry.Linked {
					t.Fatalf("not off: %+v", s.Entry)
				}
			})
		}
	}
}

func TestEntryStatusPreservesNativeConflictWithOtherBackendLink(t *testing.T) {
	m, root := fixture(t, false)
	put(t, root, entryPath+"/index.html", "native")
	symlink(t, root, "/data/adb/modules/teesim/action.sh", m.linkSource("action.sh"))
	entry := getStatus(t, m).Entry
	if !entry.Linked || !entry.HasOwnWebUI {
		t.Fatalf("lost native conflict: %+v", entry)
	}
	if err := m.entrySet(false); err != nil {
		t.Fatal(err)
	}
	if read(t, root, entryPath+"/index.html") != "native" {
		t.Fatal("native WebUI changed")
	}
}
