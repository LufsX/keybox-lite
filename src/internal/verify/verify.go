// Package verify downloads Google's revocation list and checks a keybox against it.
//
// Networking goes through curl/wget via os/exec: the shell already solves Android's DNS
// and TLS, and os/exec costs almost nothing in binary size, unlike net/http.
package verify

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

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
		cmd := exec.Command(curl, "-fsSL", "--connect-timeout", "20", "--max-time", "180",
			"-H", "Accept-Encoding: identity", "-o", out, url)
		if cmd.Run() == nil {
			return nil
		}
	}
	if bb := busybox(); bb != "" {
		cmd := exec.Command(bb, "wget", "-q", "-T", "60", "-t", "1",
			"--header", "Accept-Encoding: identity", "-O", out, url)
		if cmd.Run() == nil {
			return nil
		}
	}
	if wget, err := exec.LookPath("wget"); err == nil {
		cmd := exec.Command(wget, "-q", "--timeout=60", "--tries=1", "-O", out, url)
		if cmd.Run() == nil {
			return nil
		}
	}
	return fmt.Errorf("no downloader worked for %s", url)
}

func looksLikeStatus(path string) bool {
	b, err := os.ReadFile(path)
	return err == nil && len(b) > 0 && strings.Contains(string(b), "entries")
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

// Run: 0 = not revoked, 1 = revoked/suspended, 2 = could not check.
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
	serials := keybox.Serials(data)
	if len(serials) == 0 {
		fmt.Println("could not parse certificate serials")
		return 2
	}

	out := tmpDir() + "/keybox-verify.json"
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
	defer os.Remove(out)
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
	fmt.Printf("revocation: %d certs checked, none revoked\n", len(serials))
	fmt.Println("RESULT: not revoked")
	return 0
}
