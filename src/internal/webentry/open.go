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

// Run opens only this module or a supported backend with an injected entry.
func Run(args []string) int {
	moduleID := "keybox-lite"
	if len(args) != 0 {
		if len(args) != 2 || args[0] != "--module" {
			fmt.Fprintln(os.Stderr, "usage: open-webui [--module backend-id]")
			return 2
		}
		switch args[1] {
		case "tricky_store", "oh_my_keymint", "teesim":
			moduleID = args[1]
		default:
			fmt.Fprintln(os.Stderr, "unsupported WebUI entry module")
			return 2
		}
	}
	if err := openModule(os.Stdout, command, moduleID); err != nil {
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
	return openModule(out, run, "keybox-lite")
}

func openModule(out io.Writer, run func(string, ...string) ([]byte, error), moduleID string) error {
	var launchFailed bool
	for _, h := range hosts {
		raw, err := run("pm", "path", h.name)
		if err != nil || !strings.HasPrefix(strings.TrimSpace(string(raw)), "package:") {
			continue
		}
		raw, err = run("am", "start", "-n", h.component, "-e", h.extra, moduleID)
		message := string(raw)
		if err != nil || strings.Contains(message, "Error:") || strings.Contains(message, "Error type") ||
			strings.Contains(message, "Exception") {
			launchFailed = true
			continue
		}
		if moduleID == "keybox-lite" {
			fmt.Fprintln(out, "Opened Keybox Lite WebUI.")
		} else {
			fmt.Fprintf(out, "Opened %s WebUI.\n", moduleID)
		}
		return nil
	}
	if launchFailed {
		return fmt.Errorf("could not open the installed WebUI host")
	}
	return fmt.Errorf("install KSUWebUIStandalone or WebUI X to open the WebUI from Magisk")
}
