package runtime

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkipRevocationSettingsAndMigration(t *testing.T) {
	ctx := context.Background()
	for _, value := range []string{"", "0", "1", "true", "2", "01"} {
		t.Run("legacy_"+value, func(t *testing.T) {
			e := fixture(t)
			raw := "SCHEDULE=weekly\nLAST_RUN=123\nSOURCE=spectre\n"
			if value != "" {
				raw += "SKIP_REVOCATION=" + value + "\n"
			}
			mustWrite(t, e.legacyConf(), []byte(raw))
			c, err := e.readConfig()
			if err != nil || c.skipRevocation != (value == "1") {
				t.Fatalf("config %+v: %v", c, err)
			}
			if err := e.settings(ctx, []string{"source", "meow"}); err != nil {
				t.Fatal(err)
			}
			if exists(e.legacyConf()) {
				t.Fatal("legacy config not migrated")
			}
			c, err = e.readConfig()
			if err != nil || c.schedule != "weekly" || c.last != 123 || c.source != "meow" || c.skipRevocation != (value == "1") {
				t.Fatalf("migration lost fields: %+v %v", c, err)
			}
			for _, setting := range []string{"1", "0"} {
				if err := e.settings(ctx, []string{"skip-revocation", setting}); err != nil {
					t.Fatal(err)
				}
				if err := e.settings(ctx, []string{"set", "daily"}); err != nil {
					t.Fatal(err)
				}
				e.out.(*bytes.Buffer).Reset()
				if err := e.settings(ctx, []string{"get"}); err != nil {
					t.Fatal(err)
				}
				want := "SCHEDULE=daily\nLAST_RUN=123\nSOURCE=meow\nSKIP_REVOCATION=" + setting + "\n"
				if got := e.out.(*bytes.Buffer).String(); got != want {
					t.Fatalf("get %q want %q", got, want)
				}
			}
		})
	}
	for _, args := range [][]string{
		{"settings", "/module", "skip-revocation"},
		{"settings", "/module", "skip-revocation", "2"},
		{"settings", "/module", "skip-revocation", "true"},
		{"settings", "/module", "skip-revocation", "01"},
		{"settings", "/module", "skip-revocation", "1", "extra"},
	} {
		if code := Run(args); code != 2 {
			t.Fatalf("%v: %d", args, code)
		}
	}
}

func TestUpdateSkipRevocationSnapshotWithExplicitSources(t *testing.T) {
	e := fixture(t)
	ctx := context.Background()
	if err := e.init(); err != nil {
		t.Fatal(err)
	}
	if err := e.writeConfig(config{schedule: "daily", source: "spectre", skipRevocation: true}); err != nil {
		t.Fatal(err)
	}
	e.meow = "https://meow/snapshot"
	setResponses(t, e, map[string]response{e.meow: {Body: meowEncode("<AndroidAttestation>synthetic</AndroidAttestation>")}})
	calls := 0
	e.d.verify = func(_ context.Context, _ string, _ string, _ io.Writer) int {
		calls++
		if !e.skipRevocation {
			t.Error("saved policy ignored with explicit sources")
		}
		// A concurrent settings change must affect only the next update.
		if err := e.settings(ctx, []string{"skip-revocation", "0"}); err != nil {
			t.Error(err)
		}
		if !e.skipRevocation {
			t.Error("in-flight snapshot changed")
		}
		return 0
	}
	if err := e.update(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("verify calls %d", calls)
	}
	log := read(t, filepath.Join(e.p.tmp, "keybox-update.log"))
	if !strings.Contains(log, "local certificate checks") || !strings.Contains(log, "revocation skipped") {
		t.Fatalf("misleading log: %s", log)
	}
	e.d.verify = func(_ context.Context, _ string, _ string, _ io.Writer) int {
		if e.skipRevocation {
			t.Error("next update did not reload setting")
		}
		return 2
	}
	if err := e.update(ctx); err == nil {
		t.Fatal("verification failure accepted")
	}
}

func TestScheduledUpdatePreservesSkipRevocation(t *testing.T) {
	e := fixture(t)
	ctx := context.Background()
	if err := e.init(); err != nil {
		t.Fatal(err)
	}
	if err := e.writeConfig(config{schedule: "daily", source: "meow", skipRevocation: true}); err != nil {
		t.Fatal(err)
	}
	e.d.update = func(ctx context.Context) error {
		// Simulate changing policy while a scheduled update is running.
		return e.settings(ctx, []string{"skip-revocation", "0"})
	}
	if err := e.tick(ctx, config{schedule: "daily", source: "meow", skipRevocation: true}); err != nil {
		t.Fatal(err)
	}
	c, err := e.readConfig()
	if err != nil || c.skipRevocation || c.last != e.d.now().Unix() || c.source != "meow" || c.schedule != "daily" {
		t.Fatalf("scheduled write lost latest config: %+v %v", c, err)
	}
}

func TestRealUpdateSkipRevocationOffline(t *testing.T) {
	e := fixture(t)
	realCommands(t, e)
	if err := e.init(); err != nil {
		t.Fatal(err)
	}
	if err := e.writeConfig(config{schedule: "off", source: "spectre", skipRevocation: true}); err != nil {
		t.Fatal(err)
	}
	setResponses(t, e, map[string]response{e.meow: {Body: meowEncode(syntheticKeybox(t))}})
	if err := e.update(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, e.out)
	}
	log := read(t, filepath.Join(e.p.tmp, "keybox-update.log"))
	for _, want := range []string{"RESULT: skipped (revocation)", "[SKIP] revocation:", "local certificate checks"} {
		if !strings.Contains(log, want) {
			t.Fatalf("missing %q: %s", want, log)
		}
	}
	if strings.Contains(log, "not revoked") || strings.Contains(log, "none revoked") {
		t.Fatalf("false revocation claim: %s", log)
	}
	if got := read(t, e.host("/data/adb/tricky_store/keybox.xml")); got == "OLD" {
		t.Fatal("local verification did not install")
	}
}

func TestRuntimeVerifySkipArgumentContract(t *testing.T) {
	e := fixture(t)
	e.p.bin = filepath.Join(e.p.root, "verify-args")
	mustWrite(t, e.p.bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"))
	// The wrapper records argument boundaries, including a file containing spaces.
	if err := os.Chmod(e.p.bin, 0700); err != nil {
		t.Fatal(err)
	}
	for _, skip := range []bool{false, true} {
		e.skipRevocation = skip
		var out bytes.Buffer
		if code := e.verify(context.Background(), "candidate file", e.p.tmp, &out); code != 0 {
			t.Fatalf("verify exit %d", code)
		}
		want := "verify\ncandidate file\n"
		if skip {
			want = "verify\n--skip-revocation\ncandidate file\n"
		}
		if out.String() != want {
			t.Fatalf("args %q want %q", out.String(), want)
		}
	}
}
