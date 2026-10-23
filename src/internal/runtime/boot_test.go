package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeBootFailureKeepsSchedule(t *testing.T) {
	for _, schedule := range []string{"off", "daily"} {
		t.Run(schedule, func(t *testing.T) {
			e := fixture(t)
			e.init()
			e.writeConfig(config{schedule, 0})
			boots, launches, ticks := 0, 0, 0
			e.d.boot = func(string) error {
				boots++
				if schedule == "daily" && launches == 0 {
					t.Error("Boot blocked scheduler launch")
				}
				return fmt.Errorf("pm temporarily unavailable")
			}
			e.d.launch = func(_ string, args ...string) error {
				launches++
				if args[0] == "maintenance" {
					if len(args) != 2 {
						t.Errorf("maintenance args %q", args)
					}
				} else if len(args) != 3 || args[2] != "--worker" {
					t.Errorf("worker args %q", args)
				}
				return nil
			}
			if err := e.service(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			if boots != 1 {
				t.Fatal("Boot not attempted exactly once")
			}
			wantLaunch := 1
			if schedule == "daily" {
				wantLaunch = 2
			}
			if launches != wantLaunch {
				t.Fatalf("launches %d want %d", launches, wantLaunch)
			}
			if !strings.Contains(fmt.Sprint(e.out), "[WARN] Target maintenance: pm temporarily unavailable") {
				t.Fatal("Boot error was silent")
			}
			if schedule == "daily" {
				// The real worker loop must still perform a due tick after Boot failed.
				e.d.update = func(context.Context) error { ticks++; return e.writeConfig(config{"off", 0}) }
				if err := e.service(context.Background(), true); err != nil {
					t.Fatal(err)
				}
				if ticks != 1 {
					t.Fatal("Boot failure blocked due update")
				}
			}
		})
	}
}
func TestRuntimeBootWarningDoesNotHideConfigError(t *testing.T) {
	e := fixture(t)
	e.init()
	e.d.boot = func(string) error { return fmt.Errorf("target restore failed") }
	os.Symlink(filepath.Join(e.p.root, "missing"), e.p.conf)
	if err := e.service(context.Background(), false); err == nil {
		t.Fatal("invalid configuration ignored")
	}
}
