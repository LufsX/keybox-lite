package updatejob

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func waitRecord(t *testing.T, dir string) record {
	t.Helper()
	for i := 0; i < 200; i++ {
		b, e := os.ReadFile(filepath.Join(dir, "update-job.json"))
		var r record
		if e == nil && json.Unmarshal(b, &r) == nil && same(r) {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not start")
	return record{}
}
func TestCancelStopsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "action.sh")
	os.WriteFile(script, []byte("trap '' TERM\nsleep 30 &\necho $! > '"+dir+"/child'\nwait\n"), 0600)
	done := make(chan int, 1)
	go func() { done <- run(script, dir, time.Minute, time.Minute) }()
	r := waitRecord(t, dir)
	if rc := run(script, dir, time.Minute, time.Minute); rc != 1 {
		t.Fatalf("duplicate run=%d", rc)
	}
	if rc := Cancel([]string{dir}); rc != 0 {
		t.Fatalf("cancel=%d", rc)
	}
	select {
	case rc := <-done:
		if rc == 0 {
			t.Fatal("cancelled job reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("job stuck")
	}
	if same(r) {
		t.Fatal("root job still alive")
	}
	child, _ := os.ReadFile(filepath.Join(dir, "child"))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(child)))
	if pid > 1 {
		b, e := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		if e == nil && !strings.Contains(string(b), ") Z ") {
			t.Fatal("download child still executing")
		}
	}
	if _, e := os.Stat(filepath.Join(dir, "update-job.json")); !os.IsNotExist(e) {
		t.Fatal("job record leaked")
	}
	if rc := Cancel([]string{dir}); rc != 0 {
		t.Fatal("idle cancel failed")
	}
}
func TestDeadlineAndStalePID(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "action.sh")
	os.WriteFile(script, []byte("sleep 30\n"), 0600)
	if rc := run(script, dir, 100*time.Millisecond, time.Minute); rc != 130 {
		t.Fatalf("deadline=%d", rc)
	}
	b, _ := json.Marshal(record{PID: os.Getpid(), Start: "incorrect-start-token"})
	os.WriteFile(filepath.Join(dir, "update-job.json"), b, 0600)
	if rc := Cancel([]string{dir}); rc == 0 {
		t.Fatal("accepted stale identity")
	}
}
