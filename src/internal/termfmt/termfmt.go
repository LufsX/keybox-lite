// Package termfmt formats human-readable terminal logs.
package termfmt

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// Printf writes a human-readable log to stdout, coloring status labels on a TTY.
func Printf(format string, args ...any) {
	write(os.Stdout, fmt.Sprintf(format, args...))
}

// Println writes a human-readable log line to stdout.
func Println(args ...any) {
	write(os.Stdout, fmt.Sprintln(args...))
}

func write(out *os.File, message string) {
	if enabled(out) {
		lines := strings.SplitAfter(message, "\n")
		for i, line := range lines {
			lines[i] = colorize(line)
		}
		message = strings.Join(lines, "")
	}
	fmt.Fprint(out, message)
}

func enabled(out *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	var termios syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, out.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&termios)))
	return errno == 0
}

func colorize(line string) string {
	end, color := 0, ""
	if strings.HasPrefix(line, "[") {
		if i := strings.IndexByte(line, ']'); i >= 0 {
			end = i + 1
			switch line[1:i] {
			case "OK", "DONE":
				color = "32"
			case "FAIL", "REVOKED", "revoked":
				color = "31"
			case "WAIT", "STOP", "WARN", "UNKNOWN", "unknown":
				color = "33"
			case "FETCH", "CHECK", "INSTALL":
				color = "36"
			case "CACHE":
				color = "35"
			}
		}
	} else if strings.HasPrefix(line, "RESULT: ") {
		value := strings.TrimPrefix(line, "RESULT: ")
		for _, state := range []struct{ value, color string }{
			{"not revoked", "32"},
			{"revoked", "31"},
			{"expired", "31"},
			{"not_yet_valid", "31"},
			{"unknown", "33"},
		} {
			if strings.HasPrefix(value, state.value) {
				rest := value[len(state.value):]
				if rest == "" || strings.ContainsAny(rest[:1], " \t\r\n") {
					end, color = len("RESULT: ")+len(state.value), state.color
					break
				}
			}
		}
	}
	if color == "" {
		return line
	}
	return "\x1b[" + color + "m" + line[:end] + "\x1b[0m" + line[end:]
}
