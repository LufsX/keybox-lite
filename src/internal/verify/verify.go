// Package verify downloads Google's revocation list and checks a keybox against it.
//
// Networking goes through curl/wget via os/exec: the shell already solves Android's DNS
// and TLS, and os/exec costs almost nothing in binary size, unlike net/http.
package verify

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"keyboxlite/internal/info"
	"keyboxlite/internal/keybox"
	"keyboxlite/internal/status"
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

func busybox() string {
	for _, p := range []string{
		"/data/adb/ksu/bin/busybox", "/data/adb/ap/bin/busybox",
		"/data/adb/magisk/busybox", "/system/bin/busybox", "/system/xbin/busybox",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("busybox"); err == nil {
		return p
	}
	return ""
}

func fetch(url, out string) error {
	os.Remove(out)
	if curl, err := exec.LookPath("curl"); err == nil {
		cmd := exec.Command(curl, "-fsSL", "--connect-timeout", "8", "--max-time", "60", "--speed-time", "15", "--speed-limit", "128",
			"-H", "Accept-Encoding: identity", "-o", out, url)
		if cmd.Run() == nil {
			return nil
		}
	}
	if bb := busybox(); bb != "" {
		cmd := exec.Command(bb, "wget", "-q", "-T", "20",
			"--header", "Accept-Encoding: identity", "-O", out, url)
		if cmd.Run() == nil {
			return nil
		}
	}
	if wget, err := exec.LookPath("wget"); err == nil {
		cmd := exec.Command(wget, "-q", "-T", "20", "-O", out, url)
		if cmd.Run() == nil {
			return nil
		}
	}
	return fmt.Errorf("no downloader worked for %s", url)
}

func looksLikeStatus(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	_, err = status.Check(b, nil)
	return err == nil
}

func candidates(accel string) []string {
	var urls []string
	for _, p := range strings.Fields(accel) {
		urls = append(urls, p+live)
	}
	urls = append(urls, live)
	for _, p := range strings.Fields(accel) {
		urls = append(urls, p+mirror)
	}
	urls = append(urls, "https://gh-proxy.com/"+mirror, mirror)
	return urls
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
		fmt.Printf("no keybox found (%s)\n", kbPath)
		return 2
	}
	fmt.Printf("keybox: %s\n", kbPath)
	if !keybox.StructureValid(data) {
		fmt.Println("not a keybox (missing AndroidAttestation)")
		return 2
	}
	serials, err := keybox.ParseSerials(data)
	if err != nil || len(serials) == 0 {
		fmt.Println("could not parse certificate serials")
		return 2
	}

	validity, err := keybox.CheckValidity(data, time.Now())
	if err != nil {
		fmt.Printf("could not parse certificate validity: %v\n", err)
		return 2
	}
	for _, cert := range validity.Certificates {
		fmt.Printf("validity: serial %s -> %s (%s to %s)\n", cert.Serial, cert.Status,
			time.Unix(cert.NotBefore, 0).UTC().Format(time.RFC3339), time.Unix(cert.NotAfter, 0).UTC().Format(time.RFC3339))
	}
	if validity.Status != "valid" {
		fmt.Printf("RESULT: %s (device time)\n", validity.Status)
		return 3
	}

	dir, err := os.MkdirTemp(tmpDir(), "keybox-verify-")
	if err != nil {
		fmt.Println("could not create verification workdir")
		return 2
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "status.json")
	used := ""
	stale := false
	for _, u := range candidates(accel) {
		if fetch(u, out) == nil && looksLikeStatus(out) {
			used = u
			stale = strings.Contains(u, "keybox-tools")
			break
		}
	}
	if used == "" {
		fmt.Println("could not download Google status")
		return 2
	}
	fmt.Printf("status: %s\n", used)
	if stale {
		fmt.Println("note: stale mirror, newer revocations may be missing")
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		return 2
	}
	revoked, err := status.Check(raw, serials)
	if err != nil {
		fmt.Printf("status: %v\n", err)
		return 2
	}
	if len(revoked) > 0 {
		for _, e := range revoked {
			fmt.Printf("REVOKED: serial %s -> %s %s\n", e.Serial, e.Status, e.Reason)
		}
		fmt.Println("RESULT: revoked")
		return 1
	}
	if stale {
		fmt.Println("RESULT: unknown (serial absent from stale mirror; current revocation status unavailable)")
		return 2
	}
	fmt.Printf("revocation: %d certs checked, none revoked\n", len(serials))
	fmt.Println("RESULT: not revoked")
	return 0
}
