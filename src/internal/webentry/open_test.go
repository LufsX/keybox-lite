package webentry

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
)

func TestOpenHostFallbackAndFixedModule(t *testing.T) {
	var calls [][]string
	var out bytes.Buffer
	err := open(&out, func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		if name == "pm" {
			return []byte("package:/data/app/base.apk\n"), nil
		}
		if args[2] == hosts[0].component {
			return []byte("Error type 3\n"), nil
		}
		return []byte("Starting: Intent"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 || !reflect.DeepEqual(calls[3], []string{"am", "start", "-n", hosts[1].component, "-e", "MOD_ID", "keybox-lite"}) {
		t.Fatalf("unexpected host fallback: %v", calls)
	}
	if out.String() != "Opened Keybox Lite WebUI.\n" {
		t.Fatal(out.String())
	}
}

func TestMissingHostDoesNotLaunchOrInstall(t *testing.T) {
	for _, output := range []string{"", "Error: package unavailable"} {
		err := open(&bytes.Buffer{}, func(name string, args ...string) ([]byte, error) {
			if name != "pm" {
				t.Fatalf("unexpected command %s", name)
			}
			return []byte(output), nil
		})
		if err == nil {
			t.Fatal("missing host reported success")
		}
	}
	err := open(&bytes.Buffer{}, func(name string, args ...string) ([]byte, error) {
		if name == "pm" {
			return []byte("package:/data/app/base.apk"), nil
		}
		return nil, fmt.Errorf("launch failed")
	})
	if err == nil {
		t.Fatal("launch error reported success")
	}
}
