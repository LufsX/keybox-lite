// Package info reports the installed keybox as JSON (used by the WebUI).
package info

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"keyboxlite/internal/backend"
)

// DetectPath returns the selected backend's device path, or an empty string if
// no supported module or safe target exists. It shares install-time detection.
func DetectPath() string {
	report, err := backend.Detect("/")
	if err != nil || report.Selected == nil {
		return ""
	}
	return report.Selected.KeyboxPath
}

// Run prints a JSON status object and returns an exit code.
func Run(args []string) int {
	root, override := "/", ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--root" {
			i++
			if i >= len(args) || args[i] == "" {
				fmt.Fprintln(os.Stderr, "info: --root needs a directory")
				return 2
			}
			root = args[i]
		} else if override == "" && !strings.HasPrefix(args[i], "--") {
			override = args[i]
		} else {
			fmt.Fprintln(os.Stderr, "usage: info [keybox] [--root <sysroot>]")
			return 2
		}
	}
	out, err := json.Marshal(collect(root, override))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(string(out))
	return 0
}
