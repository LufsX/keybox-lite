package runtime

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func syntheticKeybox(t *testing.T) string {
	t.Helper()
	issue := func(private, public any) string {
		cert := &x509.Certificate{SerialNumber: big.NewInt(42), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
		der, err := x509.CreateCertificate(rand.Reader, cert, cert, public, private)
		if err != nil {
			t.Fatal(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecPriv, err := x509.MarshalECPrivateKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}
	ecPEM := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: ecPriv}))
	ecCert := issue(ecKey, &ecKey.PublicKey)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)}))
	rsaCert := issue(rsaKey, &rsaKey.PublicKey)
	// TEESimulator requires both algorithms, each with a leaf and an issuer.
	return fmt.Sprintf("<AndroidAttestation><NumberOfKeyboxes>1</NumberOfKeyboxes><Keybox DeviceID=\"synthetic\">"+
		"<Key algorithm=\"ecdsa\"><PrivateKey format=\"pem\">%s</PrivateKey><CertificateChain><NumberOfCertificates>2</NumberOfCertificates><Certificate format=\"pem\">%s</Certificate><Certificate format=\"pem\">%s</Certificate></CertificateChain></Key>"+
		"<Key algorithm=\"rsa\"><PrivateKey format=\"pem\">%s</PrivateKey><CertificateChain><NumberOfCertificates>2</NumberOfCertificates><Certificate format=\"pem\">%s</Certificate><Certificate format=\"pem\">%s</Certificate></CertificateChain></Key>"+
		"</Keybox></AndroidAttestation>\n", ecPEM, ecCert, ecCert, rsaPEM, rsaCert, rsaCert)
}
func realCommands(t *testing.T, e *engine) {
	t.Helper()
	t.Setenv("KBL_HELPER_REAL", "1")
	e.p.bin = helperWrapper(t, e.p.root, "keyboxlite", "")
	e.d.prepare = e.prepare
	e.d.verify = e.verify
	t.Setenv("PATH", e.p.root+":"+os.Getenv("PATH"))
}
func TestStandaloneRealPrepareVerifyInstall(t *testing.T) {
	for _, spec := range []string{"tricky_store 246", "tricky_store 245", "oh_my_keymint 1", "teesim 1"} {
		t.Run(spec, func(t *testing.T) {
			e := fixture(t)
			realCommands(t, e)
			os.RemoveAll(filepath.Join(e.p.root, "data/adb/modules"))
			fields := strings.Fields(spec)
			mustWrite(t, filepath.Join(e.p.root, "data/adb/modules", fields[0], "module.prop"), []byte("versionCode="+fields[1]+"\n"))
			if fields[0] == "teesim" {
				mustWrite(t, e.host("/data/adb/teesim/config.json"), []byte("{\"profiles\":{\"default\":{\"keybox\":\"nested/keybox.xml\"}}}"))
			}
			target, err := e.target()
			if err != nil {
				t.Fatal(err)
			}
			p := e.host(target.KeyboxPath)
			mustWrite(t, p, []byte("OLD"))
			os.Chmod(p, 0600)
			before, _ := os.Stat(p)
			dirty := "\ufeff<!-- synthetic watermark -->" + syntheticKeybox(t)
			setResponses(t, e, map[string]response{e.meow: {Body: meowEncode(dirty)}, "https://android.googleapis.com/attestation/status": {Body: "{\"entries\":{}}"}})
			if err = e.update(context.Background()); err != nil {
				t.Fatalf("%v\n%s", err, e.out)
			}
			data := read(t, p)
			if strings.Contains(data, "watermark") || !strings.Contains(data, "AndroidAttestation") {
				t.Fatal("prepare was skipped")
			}
			after, _ := os.Stat(p)
			a := before.Sys().(*syscall.Stat_t)
			b := after.Sys().(*syscall.Stat_t)
			if a.Uid != b.Uid || a.Gid != b.Gid || after.Mode().Perm() != 0600 {
				t.Fatal("owner/mode changed")
			}
			backups, _ := filepath.Glob(p + ".bak.*")
			if len(backups) != 1 || read(t, backups[0]) != "OLD" {
				t.Fatal("old backup missing")
			}
			log := read(t, filepath.Join(e.p.tmp, "keybox-update.log"))
			if !strings.Contains(log, "[meow / direct]") || !strings.Contains(log, "RESULT: not revoked") {
				t.Fatalf("real verify output missing:\n%s", log)
			}
			cmd := exec.Command(e.p.bin, "info", "--root", e.p.root)
			result, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			var state struct {
				Exists         bool
				StructureValid bool `json:"structure_valid"`
			}
			if err = json.Unmarshal(result, &state); err != nil || !state.Exists || !state.StructureValid {
				t.Fatalf("installed info: %s %v", result, err)
			}
			clean(t, e)
		})
	}
}
func TestStandaloneAddonAndRevocation(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(strconv.FormatBool(revoked), func(t *testing.T) {
			e := fixture(t)
			realCommands(t, e)
			e.sources = []string{"addon"}
			encoded := hex.EncodeToString([]byte(base64.StdEncoding.EncodeToString([]byte(syntheticKeybox(t)))))
			status := "{\"entries\":{}}"
			if revoked {
				status = "{\"entries\":{\"2a\":{\"status\":\"REVOKED\",\"reason\":\"KEY_COMPROMISE\"}}}"
			}
			setResponses(t, e, map[string]response{e.addon: {Body: encoded}, "https://android.googleapis.com/attestation/status": {Body: status}})
			err := e.update(context.Background())
			if revoked {
				if err == nil {
					t.Fatal("revoked certificate installed")
				}
				unchanged(t, e)
			} else {
				if err != nil {
					t.Fatalf("%v\n%s", err, e.out)
				}
				if !strings.Contains(read(t, filepath.Join(e.p.tmp, "keybox-update.history")), "addon") {
					t.Fatal("addon history")
				}
			}
		})
	}
}
func TestStandaloneCancelVerifyDescendants(t *testing.T) {
	e := fixture(t)
	realCommands(t, e)
	t.Setenv("KBL_HELPER_VERIFY_WAIT", "1")
	// Keep one route to make the exact verifier descendant observable.
	e.meow = "https://example.test/payload"
	setResponses(t, e, map[string]response{e.meow: {Body: meowEncode(syntheticKeybox(t))}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.update(ctx) }()
	deadline := time.Now().Add(4 * time.Second)
	for !exists(filepath.Join(e.p.root, "verify-child")) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !exists(filepath.Join(e.p.root, "verify-child")) {
		cancel()
		t.Fatal("verify did not launch child")
	}
	pid := read(t, filepath.Join(e.p.root, "verify-child"))
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled verifier installed")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("verify cancellation stalled")
	}
	if raw, err := os.ReadFile("/proc/" + pid + "/stat"); err == nil && !strings.Contains(string(raw), ") Z ") {
		t.Fatal("verify descendant survived")
	}
	log := read(t, filepath.Join(e.p.tmp, "keybox-update.log"))
	for _, line := range []string{"[meow / direct] [CHECK] full synthetic verify stdout", "[meow / direct] [WARN] full synthetic verify stderr"} {
		if !strings.Contains(log, line) {
			t.Fatalf("verify output lost: %s\n%s", line, log)
		}
	}
	unchanged(t, e)
}
