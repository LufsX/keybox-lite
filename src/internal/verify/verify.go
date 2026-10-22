// Package verify downloads Google's revocation list and checks a keybox against it.
//
// Downloads invoke curl/wget via os/exec.
package verify

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"keyboxlite/internal/info"
	"keyboxlite/internal/keybox"
	"keyboxlite/internal/status"
	"keyboxlite/internal/termfmt"
)

const (
	live   = "https://android.googleapis.com/attestation/status"
	mirror = "https://raw.githubusercontent.com/purainity/keybox-tools/main/res/status.json"
)

func tmpDir() string {
	if d := os.Getenv("TMPDIR"); d != "" {
		return d
	}
	return "/data/local/tmp"
}

// escapeStatusText keeps downloaded fields on one line without terminal controls.
func escapeStatusText(text string) string {
	var out strings.Builder
	for _, r := range text {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			quoted := strconv.QuoteRune(r)
			out.WriteString(quoted[1 : len(quoted)-1])
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// Run: 0 = time-valid and not revoked, 1 = revoked/suspended,
// 2 = could not check, 3 = expired or not yet valid.
func Run(args []string) int {
	accel := os.Getenv("KB_ACCEL")
	if accel == "" {
		accel = "https://c.etc.moe/"
	}
	kbPath := ""
	if len(args) > 0 {
		kbPath = args[0]
	}
	if kbPath == "" {
		kbPath = info.DetectPath()
	}
	data, err := os.ReadFile(kbPath)
	if err != nil {
		termfmt.Printf("[FAIL] could not read keybox %s: %v\n", filepath.Base(kbPath), err)
		return 2
	}
	termfmt.Printf("[CHECK] keybox: %s\n", filepath.Base(kbPath))
	if !keybox.StructureValid(data) {
		termfmt.Println("[FAIL] not a keybox (missing AndroidAttestation)")
		return 2
	}
	serials, err := keybox.ParseSerials(data)
	if err != nil || len(serials) == 0 {
		termfmt.Println("[FAIL] could not parse certificate serials")
		return 2
	}

	validity, err := keybox.CheckValidity(data, time.Now())
	if err != nil {
		termfmt.Printf("[FAIL] could not parse certificate validity: %v\n", err)
		return 2
	}
	if validity.Status == "valid" {
		termfmt.Printf("[OK] validity: valid (%d certificates)\n", len(validity.Certificates))
	} else {
		termfmt.Printf("[FAIL] validity: %s (%d certificates)\n", validity.Status, len(validity.Certificates))
	}
	for i, cert := range validity.Certificates {
		if cert.Status != "valid" {
			termfmt.Printf("[FAIL] certificate %d: %s (%s to %s UTC)\n", i+1, cert.Status,
				time.Unix(cert.NotBefore, 0).UTC().Format("2006-01-02 15:04:05"),
				time.Unix(cert.NotAfter, 0).UTC().Format("2006-01-02 15:04:05"))
		}
	}
	if validity.Status != "valid" {
		termfmt.Printf("RESULT: %s (device time)\n", validity.Status)
		return 3
	}

	integrity := keybox.CheckIntegrity(data)
	for i, key := range integrity.Keys {
		if key.PrivateKeyValid && key.KeyMatch && key.ChainValid {
			termfmt.Printf("[OK] key %d: private key matches; certificate chain signatures valid\n", i+1)
		} else {
			termfmt.Printf("[FAIL] key %d: private_key_valid=%t key_match=%t chain_valid=%t\n",
				i+1, key.PrivateKeyValid, key.KeyMatch, key.ChainValid)
		}
		termfmt.Printf("[CHECK] key %d root: %s\n", i+1, key.Root)
		if key.Root == "unknown" || key.Root == "aosp_ec" || key.Root == "aosp_rsa" {
			termfmt.Printf("[WARN] key %d: root is unknown or an AOSP software test root; hardware trust is not established\n", i+1)
		}
	}
	if integrity.Status != "valid" {
		termfmt.Println("[FAIL] keybox private key or certificate chain validation failed")
		termfmt.Println("RESULT: unknown (invalid keybox cryptography)")
		return 2
	}

	dir, err := os.MkdirTemp(tmpDir(), "keybox-verify-")
	if err != nil {
		termfmt.Printf("[FAIL] could not create verification workdir: %v\n", err)
		return 2
	}
	defer os.RemoveAll(dir)
	download, ok := fetchStatus(context.Background(), dir, accel)
	if !ok {
		termfmt.Println("[FAIL] current Google status unavailable")
		return 2
	}
	switch {
	case download.stale:
		termfmt.Printf("[CHECK] status: mirror (%s)\n", download.url)
		termfmt.Println("[WARN] stale mirror: newer revocations may be missing")
	case download.cached:
		termfmt.Printf("[CACHE] status: cache hit (<60s, %s)\n", download.url)
	default:
		termfmt.Printf("[CHECK] status: live (%s)\n", download.url)
	}

	revoked, err := status.Check(download.raw, serials)
	if err != nil {
		termfmt.Printf("[FAIL] status: %v\n", err)
		return 2
	}
	if len(revoked) > 0 {
		for _, e := range revoked {
			termfmt.Printf("[FAIL] REVOKED: serial %s -> %s %s\n", e.Serial, escapeStatusText(e.Status), escapeStatusText(e.Reason))
		}
		termfmt.Println("RESULT: revoked")
		return 1
	}
	if download.stale {
		termfmt.Println("[WARN] current revocation status unavailable for these certificates")
		termfmt.Println("RESULT: unknown (serial absent from stale mirror; current revocation status unavailable)")
		return 2
	}
	termfmt.Printf("[OK] revocation: %d certs checked, none revoked\n", len(serials))
	termfmt.Println("RESULT: not revoked")
	return 0
}
