package backend

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Run implements backend [json|target|id] [--root <sysroot>].
// Exit 0 means success, 1 means no usable target, and 2 means bad arguments.
// json succeeds when no backend exists.
func Run(args []string) int {
	return run(args, os.Stdout, os.Stderr)
}

func run(args []string, stdout, stderr io.Writer) int {
	mode, root := "json", "/"
	positional := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--root" {
			i++
			if i >= len(args) || args[i] == "" {
				fmt.Fprintln(stderr, "backend: --root needs a directory")
				return 2
			}
			root = args[i]
		} else {
			positional = append(positional, args[i])
		}
	}
	if len(positional) > 0 {
		mode = positional[0]
	}
	if len(positional) > 1 || (mode != "json" && mode != "target" && mode != "id") {
		fmt.Fprintln(stderr, "usage: backend [json|target|id] [--root <sysroot>]")
		return 2
	}
	report, err := Detect(root)
	if mode == "json" {
		if encodeErr := json.NewEncoder(stdout).Encode(report); encodeErr != nil {
			fmt.Fprintln(stderr, "backend: cannot write JSON")
			return 2
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "backend:", err)
		return 1
	}
	if mode == "json" {
		return 0
	}
	if report.Selected == nil {
		fmt.Fprintln(stderr, "backend: no supported keystore module found")
		return 1
	}
	if !report.Selected.Active {
		fmt.Fprintln(stderr, "backend: selected keystore module is disabled or pending removal")
		return 1
	}
	if mode == "target" {
		fmt.Fprintln(stdout, report.Selected.KeyboxPath)
	} else {
		fmt.Fprintln(stdout, report.Selected.Backend)
	}
	return 0
}
