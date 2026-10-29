package termfmt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

func TestStatusColors(t *testing.T) {
	for _, group := range []struct {
		color  string
		labels []string
	}{
		{"32", []string{"OK", "DONE"}},
		{"31", []string{"FAIL", "REVOKED", "revoked"}},
		{"33", []string{"WAIT", "STOP", "WARN", "UNKNOWN", "unknown"}},
		{"36", []string{"FETCH", "CHECK", "INSTALL"}},
		{"35", []string{"CACHE"}},
	} {
		for _, label := range group.labels {
			input := "[" + label + "] readable body\n"
			want := "\x1b[" + group.color + "m[" + label + "]\x1b[0m readable body\n"
			if got := colorize(input); got != want {
				t.Errorf("%s: got %q, want %q", label, got, want)
			}
		}
	}
	for _, tc := range []struct{ state, color string }{
		{"not revoked", "32"},
		{"revoked", "31"},
		{"unknown", "33"},
		{"expired", "31"},
		{"not_yet_valid", "31"},
	} {
		input := "RESULT: " + tc.state + " (details)\n"
		want := "\x1b[" + tc.color + "mRESULT: " + tc.state + "\x1b[0m (details)\n"
		if got := colorize(input); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	for _, input := range []string{
		"body [FAIL] stays plain", "[IDLE] no update", "[unfinished",
		"RESULT: revokedness", "RESULT: unknown_state", "RESULT: other",
		"{\"status\":\"revoked\"}", "<result>revoked</result>",
	} {
		if got := colorize(input); got != input {
			t.Errorf("changed unrecognized log: %q -> %q", input, got)
		}
	}
}

// Open a PTY directly so tests exercise ioctl without a shell or helper process.
func testPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("PTY unavailable: %v", err)
	}
	t.Cleanup(func() { master.Close() })
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Fatal(e)
	}
	var number uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&number))); e != 0 {
		t.Fatal(e)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slave.Close() })
	return master, slave
}

func TestTTYAndEnvironment(t *testing.T) {
	master, slave := testPTY(t)
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	if !enabled(slave) {
		t.Fatal("PTY was not detected as terminal")
	}
	write(slave, "[OK] readable")
	buf := make([]byte, 256)
	n, err := master.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(buf[:n]), "\x1b[32m[OK]\x1b[0m readable"; got != want {
		t.Fatalf("terminal output = %q, want %q", got, want)
	}
	for _, tc := range []struct{ name, value string }{
		{"NO_COLOR", "1"}, {"NO_COLOR", "0"}, {"TERM", "dumb"},
	} {
		t.Run(tc.name+"="+tc.value, func(t *testing.T) {
			t.Setenv(tc.name, tc.value)
			if enabled(slave) {
				t.Fatal("color suppression ignored")
			}
			write(slave, "[FAIL] readable")
			n, err := master.Read(buf)
			if err != nil || string(buf[:n]) != "[FAIL] readable" {
				t.Fatalf("suppressed output = %q, err = %v", buf[:n], err)
			}
		})
	}
}

func TestRedirectedOutputIsPlain(t *testing.T) {
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "")
	const message = "[OK] readable\n[CACHE] hit\nRESULT: not revoked\n"
	f, err := os.Create(filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if enabled(f) {
		t.Fatal("regular file detected as terminal")
	}
	write(f, message)
	raw, err := os.ReadFile(f.Name())
	if err != nil || string(raw) != message {
		t.Fatalf("file output = %q, err = %v", raw, err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if enabled(w) {
		t.Fatal("pipe detected as terminal")
	}
	previous := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = previous }()
	Printf("[OK] %s\n", "readable")
	Println("[CACHE] hit")
	Println("RESULT: not revoked")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	data := make([]byte, 256)
	for {
		n, err := r.Read(data)
		buf.Write(data[:n])
		if err != nil {
			break
		}
	}
	if got := buf.String(); got != message {
		t.Fatalf("pipe output = %q, want %q", got, message)
	}
}
