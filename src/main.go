// Command keyboxlite: keybox helpers for the Keybox Lite module.
//
// Networking stays in the shell; this binary is pure computation.
package main

import (
	"fmt"
	"os"

	"keyboxlite/internal/catalog"
	"keyboxlite/internal/decode"
	"keyboxlite/internal/info"
	"keyboxlite/internal/keybox"
	"keyboxlite/internal/status"
	"keyboxlite/internal/verify"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "decode-meow":
		check(decode.Meow(os.Stdin, os.Stdout))
	case "decode-spectre":
		check(decode.Spectre(os.Stdin, os.Stdout))
	case "spectre-url":
		url, src, ver, err := catalog.PickURL(os.Args[2:])
		if err != nil {
			fail(err)
		}
		fmt.Fprintf(os.Stderr, "  spectre: %s/%s\n", src, ver)
		fmt.Println(url)
	case "serials":
		kb, err := read(os.Args, 2, "serials needs <keybox>")
		if err != nil {
			fail(err)
		}
		for _, s := range keybox.Serials(kb) {
			fmt.Println(s)
		}
	case "check":
		os.Exit(runCheck(os.Args[2:]))
	case "info":
		os.Exit(info.Run(os.Args[2:]))
	case "verify":
		os.Exit(verify.Run(os.Args[2:]))
	default:
		usage()
	}
}

func runCheck(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "  (check needs <keybox> <status.json>)")
		return 2
	}
	kb, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "  (cannot read keybox)")
		return 2
	}
	if !keybox.StructureValid(kb) {
		fmt.Fprintln(os.Stderr, "  (not a keybox: missing AndroidAttestation)")
		return 2
	}
	serials := keybox.Serials(kb)
	if len(serials) == 0 {
		fmt.Fprintln(os.Stderr, "  (could not parse certificate serials)")
		return 2
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "  (could not read Google status)")
		return 2
	}
	revoked, err := status.Check(data, serials)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  (status: %v)\n", err)
		return 2
	}
	if len(revoked) > 0 {
		for _, e := range revoked {
			fmt.Printf("  REVOKED: serial %s -> %s %s\n", e.Serial, e.Status, e.Reason)
		}
		return 1
	}
	fmt.Printf("  revocation: %d certs checked, none revoked\n", len(serials))
	return 0
}

func read(args []string, n int, msg string) ([]byte, error) {
	if len(args) < n+1 {
		return nil, fmt.Errorf(msg)
	}
	return os.ReadFile(args[n])
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: keyboxlite decode-meow|decode-spectre|spectre-url|serials|check|info|verify ...")
	os.Exit(2)
}

func check(err error) {
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(2)
}
