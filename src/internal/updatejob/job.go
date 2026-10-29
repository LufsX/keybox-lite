// Package updatejob runs an update in its own process group so cancellation
// terminates the shell and its curl/wget/verification descendants together.
package updatejob

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"keyboxlite/internal/lockfile"
	"keyboxlite/internal/termfmt"
)

type record struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

func token(pid int) (string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	i := strings.LastIndex(string(b), ") ")
	if i < 0 {
		return "", errors.New("invalid process stat")
	}
	fields := strings.Fields(string(b[i+2:]))
	if len(fields) < 20 {
		return "", errors.New("invalid process stat")
	}
	return fields[19], nil
}
func same(r record) bool { t, e := token(r.PID); return e == nil && r.PID > 1 && t == r.Start }
func Run(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "run-update needs <action.sh> <state-dir>")
		return 2
	}
	return run(args[0], args[1], 20*time.Minute, 10*time.Second)
}
func run(script, state string, budget, pulse time.Duration) int {
	if err := os.MkdirAll(state, 0700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	lock, err := lockfile.Open(state, "update-control.lock")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		termfmt.Println("ERROR: another keybox update is running")
		return 1
	}
	cancelFile := filepath.Join(state, "update-cancel.json")
	os.Remove(cancelFile)
	defer os.Remove(cancelFile)
	cmd := exec.Command("sh", script, "--managed")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(signals)
	if err = cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	pid := cmd.Process.Pid
	// Always reap the process and remove surviving children, even if its shell exited first.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer syscall.Kill(-pid, syscall.SIGKILL)
	jobFile := filepath.Join(state, "update-job.json")
	defer os.Remove(jobFile)
	start, err := token(pid)
	if err != nil {
		syscall.Kill(-pid, syscall.SIGKILL)
		<-done
		return 2
	}
	raw, _ := json.Marshal(record{PID: pid, Start: start})
	tmp, err := os.CreateTemp(state, "job-*")
	if err == nil {
		_, err = tmp.Write(raw)
		closeErr := tmp.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(tmp.Name(), jobFile)
		}
		os.Remove(tmp.Name())
	}
	if err != nil {
		syscall.Kill(-pid, syscall.SIGKILL)
		<-done
		fmt.Fprintln(os.Stderr, "cannot publish update control state")
		return 2
	}
	ticker := time.NewTicker(pulse)
	defer ticker.Stop()
	deadline := time.NewTimer(budget)
	defer deadline.Stop()
	began := time.Now()
	for {
		select {
		case err := <-done:
			var cancelled record
			if data, e := os.ReadFile(cancelFile); e == nil && json.Unmarshal(data, &cancelled) == nil && cancelled.PID == pid && cancelled.Start == start {
				termfmt.Println("[STOP] Update cancelled.")
				return 130
			}
			if err == nil {
				return 0
			}
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() >= 0 {
				return exit.ExitCode()
			}
			return 130
		case <-ticker.C:
			termfmt.Printf("[WAIT] Update running · %ds elapsed\n", int(time.Since(began).Seconds()))
		case <-signals:
			termfmt.Println("[STOP] Stopping update and downloads...")
			return stop(pid, done)
		case <-deadline.C:
			termfmt.Println("[FAIL] Update exceeded 20-minute limit; stopping.")
			return stop(pid, done)
		}
	}
}
func stop(pid int, done <-chan error) int {
	syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		syscall.Kill(-pid, syscall.SIGKILL)
		<-done
	}
	return 130
}

// Cancel addresses only the recorded process group with a matching /proc start
// token. It never uses killall/pkill, which could affect unrelated downloads.
func Cancel(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "cancel-update needs <state-dir>")
		return 2
	}
	b, err := os.ReadFile(filepath.Join(args[0], "update-job.json"))
	if os.IsNotExist(err) {
		termfmt.Println("[IDLE] No update is running.")
		return 0
	}
	var r record
	if err != nil || json.Unmarshal(b, &r) != nil || !same(r) {
		fmt.Fprintln(os.Stderr, "No matching live update process.")
		return 1
	}
	group, err := syscall.Getpgid(r.PID)
	if err != nil || group != r.PID {
		fmt.Fprintln(os.Stderr, "Invalid update process group.")
		return 1
	}
	// Some shells report success after a terminated foreground job. Record the
	// request before signalling so the supervisor never reports cancelled work as success.
	if err = os.WriteFile(filepath.Join(args[0], "update-cancel.json"), b, 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !same(r) {
		termfmt.Println("[DONE] Update already finished.")
		return 0
	}
	if err = syscall.Kill(-r.PID, syscall.SIGTERM); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// The supervisor reaps the shell and kills remaining group members.
	// Escalate if a command ignores TERM and holds up the shell's traps.
	for i := 0; i < 20; i++ {
		if !same(r) {
			termfmt.Println("[STOP] Update stopped.")
			return 0
		}
		time.Sleep(100 * time.Millisecond)
	}
	if same(r) {
		if err = syscall.Kill(-r.PID, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	termfmt.Println("[STOP] Termination requested (PID " + strconv.Itoa(r.PID) + ").")
	return 0
}
