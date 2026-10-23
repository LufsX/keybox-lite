package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSourcesEveryCandidateValidated(t *testing.T) {
	e := fixture(t)
	e.sources = []string{"spectre"}
	names := []string{"partial", "decode", "prepare", "unknown", "good", "unused"}
	var entries []map[string]string
	for _, name := range names {
		entries = append(entries, map[string]string{"source": name, "version": "1"})
	}
	catalog, _ := json.Marshal(map[string]any{"entries": entries})
	responses := map[string]response{e.catalog: {Body: string(catalog)}}
	for _, name := range names {
		responses[e.base+"/"+name+"/1"] = response{Body: spectreEncode(name)}
	}
	responses[e.base+"/partial/1"] = response{Body: spectreEncode("must-not-prepare"), Code: 22}
	responses[e.base+"/decode/1"] = response{Body: "invalid"}
	setResponses(t, e, responses)
	var prepared, verified []string
	e.d.prepare = func(_ context.Context, _ string, b []byte, _ string) ([]byte, error) {
		prepared = append(prepared, string(b))
		if string(b) == "prepare" {
			return nil, fmt.Errorf("incompatible")
		}
		return b, nil
	}
	e.d.verify = func(_ context.Context, file, _ string, _ io.Writer) int {
		b := read(t, file)
		verified = append(verified, b)
		if b == "unknown" {
			return 2
		}
		return 0
	}
	if err := e.update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(prepared, ",") != "prepare,unknown,good" || strings.Join(verified, ",") != "unknown,good" {
		t.Fatalf("prepare %v verify %v", prepared, verified)
	}
	requests := read(t, filepath.Join(e.p.root, "requests"))
	if strings.Contains(requests, "unused") || strings.Count(requests, "\n") != 8 {
		t.Fatalf("fallback request order/count:\n%s", requests)
	}
}
func TestSourcesDownloadDeadline(t *testing.T) {
	e := fixture(t)
	e.meow = "https://example.test/payload"
	e.tools = e.tools[:1]
	e.downloadBudget = 80 * time.Millisecond
	setResponses(t, e, map[string]response{e.meow: {Body: meowEncode("GOOD"), Delay: 120000, Child: true}})
	e.d.prepare = func(context.Context, string, []byte, string) ([]byte, error) {
		t.Error("timed out response reached prepare")
		return nil, fmt.Errorf("bad")
	}
	started := time.Now()
	if err := e.update(context.Background()); err == nil {
		t.Fatal("timeout accepted")
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("download deadline not enforced")
	}
	pid := read(t, filepath.Join(e.p.root, "child"))
	if raw, err := os.ReadFile("/proc/" + pid + "/stat"); err == nil && !strings.Contains(string(raw), ") Z ") {
		t.Fatal("deadline left descendant")
	}
	unchanged(t, e)
}
