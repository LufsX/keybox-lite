// Package webentry opens the module in an installed standalone Android WebUI host.
package webentry

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

type host struct {
	name, component, extra string
}

var hosts = []host{
	{"io.github.a13e300.ksuwebui", "io.github.a13e300.ksuwebui/.WebUIActivity", "id"},
	{"com.dergoogler.mmrl.wx", "com.dergoogler.mmrl.wx/.ui.activity.webui.WebUIActivity", "MOD_ID"},
}

// Run never downloads or installs an application. Module IDs are fixed here so
// an entry linked into another module still opens Keybox Lite.
func Run(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "open-webui takes no arguments")
		return 2
	}
	if err := open(os.Stdout, command); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func command(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func open(out io.Writer, run func(string, ...string) ([]byte, error)) error {
	var launchFailed bool
	for _, h := range hosts {
		raw, err := run("pm", "path", h.name)
		if err != nil || !strings.HasPrefix(strings.TrimSpace(string(raw)), "package:") {
			continue
		}
		raw, err = run("am", "start", "-n", h.component, "-e", h.extra, "keybox-lite")
		message := string(raw)
		if err != nil || strings.Contains(message, "Error:") || strings.Contains(message, "Error type") ||
			strings.Contains(message, "Exception") {
			launchFailed = true
			continue
		}
		fmt.Fprintln(out, "Opened Keybox Lite WebUI.")
		return nil
	}
	if launchFailed {
		return fmt.Errorf("could not open the installed WebUI host")
	}
	return fmt.Errorf("install KSUWebUIStandalone or WebUI X to open the WebUI from Magisk")
}
