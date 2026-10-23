// Command keyboxlite: keybox helpers for the Keybox Lite module.
//
// Downloads invoke curl/wget; update jobs run and cancel dedicated process groups.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"keyboxlite/internal/backend"
	"keyboxlite/internal/catalog"
	"keyboxlite/internal/decode"
	"keyboxlite/internal/info"
	"keyboxlite/internal/keybox"
	"keyboxlite/internal/status"
	"keyboxlite/internal/targets"
	"keyboxlite/internal/updatejob"
	"keyboxlite/internal/verify"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "targets":
		os.Exit(targets.Run(os.Args[2:]))
	case "run-update":
		os.Exit(updatejob.Run(os.Args[2:]))
	case "cancel-update":
		os.Exit(updatejob.Cancel(os.Args[2:]))
	case "decode-meow":
		check(decode.Meow(os.Stdin, os.Stdout))
	case "backend":
		os.Exit(backend.Run(os.Args[2:]))
	case "prepare":
		if len(os.Args) != 3 {
			fail(errors.New("prepare needs <backend>; XML on stdin"))
		}
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 1024*1024+1))
		if err != nil {
			fail(err)
		}
		cleaned, err := keybox.Prepare(data, os.Args[2])
		if err != nil {
			fail(err)
		}
		_, err = os.Stdout.Write(cleaned)
		check(err)
	case "decode-addon":
		check(decode.Addon(os.Stdin, os.Stdout))
	case "decode-spectre":
		check(decode.Spectre(os.Stdin, os.Stdout))
	case "spectre-urls":
		urls, err := catalog.URLs(os.Args[2:])
		if err != nil {
			fail(err)
		}
		for _, url := range urls {
			fmt.Println(url)
		}
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
		serials, err := keybox.ParseSerials(kb)
		if err != nil {
			fail(err)
		}
		for _, s := range serials {
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
	serials, err := keybox.ParseSerials(kb)
	if err != nil || len(serials) == 0 {
		fmt.Fprintln(os.Stderr, "  (could not parse certificate serials)")
		return 2
	}
	validity, err := keybox.CheckValidity(kb, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "  (validity: %v)\n", err)
		return 2
	}
	if validity.Status != "valid" {
		fmt.Printf("  validity: %s (device time)\n", validity.Status)
		return 3
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
	if keybox.CheckIntegrity(kb).Status != "valid" {
		fmt.Fprintln(os.Stderr, "  (private key or certificate chain validation failed)")
		return 2
	}
	fmt.Printf("  revocation: %d certs checked, none revoked\n", len(serials))
	return 0
}

func read(args []string, n int, msg string) ([]byte, error) {
	if len(args) < n+1 {
		return nil, errors.New(msg)
	}
	return os.ReadFile(args[n])
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: keyboxlite targets|backend|prepare|decode-meow|decode-spectre|decode-addon|spectre-url|spectre-urls|serials|check|info|verify ...")
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
