#!/bin/sh
# Real Go CLI + action.sh, synthetic certificates, fake downloads, private sysroot.
set -eu
REPO=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SANDBOX=$(mktemp -d)
trap 'rm -rf "$SANDBOX"' 0
trap 'exit 1' HUP INT TERM
export SANDBOX
(cd "$REPO/src" && go build -o "$SANDBOX/keyboxlite" .)
cat > "$SANDBOX/generate.go" <<'GO'
package main
import("crypto/ecdsa";"crypto/elliptic";"crypto/rand";"crypto/x509";"encoding/base64";"encoding/hex";"encoding/pem";"fmt";"math/big";"os";"path/filepath";"strings";"time")
func main(){
 key,err:=ecdsa.GenerateKey(elliptic.P256(),rand.Reader);if err!=nil{panic(err)}
 priv,err:=x509.MarshalECPrivateKey(key);if err!=nil{panic(err)}
 cert:=&x509.Certificate{SerialNumber:big.NewInt(42),NotBefore:time.Now().Add(-time.Hour),NotAfter:time.Now().Add(time.Hour),IsCA:true,BasicConstraintsValid:true,KeyUsage:x509.KeyUsageCertSign}
 der,err:=x509.CreateCertificate(rand.Reader,cert,cert,&key.PublicKey,key);if err!=nil{panic(err)}
 p:=string(pem.EncodeToMemory(&pem.Block{Type:"EC PRIVATE KEY",Bytes:priv}))
 c:=string(pem.EncodeToMemory(&pem.Block{Type:"CERTIFICATE",Bytes:der}))
 xml:=fmt.Sprintf("<AndroidAttestation><NumberOfKeyboxes>1</NumberOfKeyboxes><Keybox DeviceID=\"synthetic\"><Key algorithm=\"ecdsa\"><PrivateKey format=\"pem\">%s</PrivateKey><CertificateChain><NumberOfCertificates>2</NumberOfCertificates><Certificate format=\"pem\">%s</Certificate><Certificate format=\"pem\">%s</Certificate></CertificateChain></Key></Keybox></AndroidAttestation>\n",p,c,c)
 root:=os.Getenv("SANDBOX")
 save:=func(name string,b []byte){if err:=os.WriteFile(filepath.Join(root,name),b,0600);err!=nil{panic(err)}}
 save("keybox.xml",[]byte(xml))
 // Feed comments/zero-width watermark through prepare before installation.
 dirty:="\ufeff<!-- synthetic watermark -->"+strings.Replace(xml,"<Keybox","<Key\u200bbox",1)
 rot:=[]byte(dirty)
 for i,c:=range rot{if c>='a'&&c<='z'{rot[i]='a'+(c-'a'+13)%26}else if c>='A'&&c<='Z'{rot[i]='A'+(c-'A'+13)%26}}
 meow:=[]byte(hex.EncodeToString(rot))
 for i:=0;i<10;i++{meow=[]byte(base64.StdEncoding.EncodeToString(meow))}
 save("meow.enc",meow)
 save("addon.enc",[]byte(hex.EncodeToString([]byte(base64.StdEncoding.EncodeToString([]byte(dirty))))))
}
GO
go run "$SANDBOX/generate.go"
mkdir -p "$SANDBOX/mock"
cat > "$SANDBOX/mock/curl" <<'MOCK'
#!/bin/sh
out=
url=
while [ "$#" -gt 0 ]; do
    case "$1" in -o) shift; out=$1 ;; https://*) url=$1 ;; esac
    shift
done
if [ "${CANCEL_PAUSE:-0}" = 1 ]; then
    sleep 120 &
    echo $! >> "$ROOT/download-pid"
    : > "$ROOT/download-started"
    wait
fi
case "$url" in
    *attestation/status)
        if [ -n "$out" ] && [ "$out" != - ]; then printf '%s' "$STATUS_JSON" > "$out"
        else printf '%s' "$STATUS_JSON"; fi
        ;;
    *Megatron)
        [ "${ADDON_ONLY:-0}" != 1 ] || exit 22
        if [ "${DISABLE_DURING_DOWNLOAD:-0}" = 1 ]; then : > "$ROOT/data/adb/modules/tricky_store/disable"; fi
        cp "$SANDBOX/meow.enc" "$out"
        ;;
    */.extra) cp "$SANDBOX/addon.enc" "$out" ;;
    *) exit 22 ;;
esac
MOCK
chmod +x "$SANDBOX/mock/curl"
# Block every downloader fallback too: tests must never use the network.
for cmd in wget busybox; do
    printf '#!/bin/sh\nexit 1\n' > "$SANDBOX/mock/$cmd"
    chmod +x "$SANDBOX/mock/$cmd"
done
export PATH="$SANDBOX/mock:$PATH"
export STATUS_JSON='{"entries":{}}'
fixture() {
    ROOT="$SANDBOX/$1"
    export ROOT
    export KB_STATUS_CACHE="$ROOT/data/adb/keybox-lite/status-cache"
    mkdir -p "$ROOT/mod/bin" "$ROOT/data/adb/modules/$2" "$ROOT/data/local/tmp"
    printf 'id=%s\nversionCode=%s\n' "$2" "$3" > "$ROOT/data/adb/modules/$2/module.prop"
    for file in action.sh sources.sh settings.sh; do
        sed "s|/data/|$ROOT/data/|g" "$REPO/$file" > "$ROOT/mod/$file"
    done
    cat > "$ROOT/mod/bin/keyboxlite" <<'WRAPPER'
#!/bin/sh
if [ "$1" = backend ]; then
    # Production device paths must resolve under the sandbox for shell writes.
    "$SANDBOX/keyboxlite" "$@" --root "$ROOT" > "$ROOT/backend-result" || exit $?
    sed "s|^/data/|$ROOT/data/|" "$ROOT/backend-result"
else
    exec "$SANDBOX/keyboxlite" "$@"
fi
WRAPPER
    chmod +x "$ROOT/mod/bin/keyboxlite"
    TMPDIR="$ROOT/data/local/tmp"
    export TMPDIR
}
action() { sh "$ROOT/mod/action.sh" > "$ROOT/action.log" 2>&1; }
for spec in 'ts tricky_store 246' 'legacy tricky_store 245' 'omk oh_my_keymint 1' 'tees teesim 1'; do
    set -- $spec
    fixture "$1" "$2" "$3"
    if [ "$1" = tees ]; then
        mkdir -p "$ROOT/data/adb/teesim"
        printf '%s' '{"profiles":{"default":{"keybox":"custom path/device.xml"}}}' > "$ROOT/data/adb/teesim/config.json"
    fi
    target=$("$SANDBOX/keyboxlite" backend target --root "$ROOT")
    target="$ROOT$target"
    mkdir -p "$(dirname "$target")"
    printf 'old synthetic installed data' > "$target"
    chmod 600 "$target"
    cp "$target" "$ROOT/old"
    if ! action; then cat "$ROOT/action.log"; exit 1; fi
    [ "$(stat -c %a "$target")" = 600 ]
    "$SANDBOX/keyboxlite" check "$target" /dev/stdin <<'STATUS'
{"entries":{}}
STATUS
    if grep -q 'watermark' "$target"; then echo 'FAIL: cleanup missed comment'; exit 1; fi
    backups=0
    for backup in "$target".bak.*; do cmp "$backup" "$ROOT/old"; backups=$((backups+1)); done
    [ "$backups" = 1 ]
    "$SANDBOX/keyboxlite" info --root "$ROOT" > "$ROOT/info.json"
    grep -q '"exists":true' "$ROOT/info.json"
    printf 'ok: real prepare/verify/install/info for %s\n' "$1"
done
fixture missing-parent teesim 1
mkdir -p "$ROOT/data/adb/teesim"
chmod 750 "$ROOT/data/adb/teesim"
printf '%s' '{"profiles":{"default":{"keybox":"new/nested/keybox.xml"}}}' > "$ROOT/data/adb/teesim/config.json"
action
[ "$(stat -c %a "$ROOT/data/adb/teesim")" = 750 ]
[ "$(stat -c %a "$ROOT/data/adb/teesim/new")" = 755 ]
[ "$(stat -c %a "$ROOT/data/adb/teesim/new/nested")" = 755 ]
[ "$(stat -c %a "$ROOT/data/adb/teesim/new/nested/keybox.xml")" = 644 ]
echo 'ok: new backend target directories are traversable; existing directory mode retained'

fixture addon tricky_store 246
# Enable Addon to test its decoder and installation with a synthetic payload.
sed 's/^SOURCES="meow spectre"$/SOURCES="meow spectre addon"/' "$ROOT/mod/action.sh" > "$ROOT/mod/action.tmp"
mv "$ROOT/mod/action.tmp" "$ROOT/mod/action.sh"
export ADDON_ONLY=1
action
grep -q 'addon.*installed$' "$TMPDIR/keybox-update.history"
echo 'ok: real Addon decode and fallback install after earlier downloads fail'
unset ADDON_ONLY

fixture cancel-download tricky_store 246
mkdir -p "$ROOT/data/adb/tricky_store"
printf old > "$ROOT/data/adb/tricky_store/keybox.xml"
export CANCEL_PAUSE=1
action &
job=$!
tries=0
while [ ! -e "$ROOT/download-started" ]; do
    tries=$((tries+1)); [ "$tries" -lt 100 ] || { kill "$job"; exit 1; }
    sleep 0.05
done
"$SANDBOX/keyboxlite" cancel-update "$ROOT/data/adb/keybox-lite"
if wait "$job"; then echo 'FAIL: cancelled update succeeded'; cat "$ROOT/action.log"; exit 1; fi
[ "$(cat "$ROOT/data/adb/tricky_store/keybox.xml")" = old ]
[ ! -e "$ROOT/data/adb/keybox-lite/update-job.json" ]
for child in $(cat "$ROOT/download-pid"); do
    if [ -f "/proc/$child/stat" ] && ! grep -q ') Z ' "/proc/$child/stat"; then echo 'FAIL: download child survived'; exit 1; fi
done
[ -z "$(find "$TMPDIR" -name 'keybox-lite.*' -type d -print)" ]
unset CANCEL_PAUSE
action
echo 'ok: stalled download cancellation kills descendants, retains old keybox and releases locks for retry'

fixture disabled-during-download tricky_store 246
mkdir -p "$ROOT/data/adb/tricky_store"
printf old > "$ROOT/data/adb/tricky_store/keybox.xml"
export DISABLE_DURING_DOWNLOAD=1
if action; then echo 'FAIL: disabled backend was written'; exit 1; fi
[ "$(cat "$ROOT/data/adb/tricky_store/keybox.xml")" = old ]
[ ! -e "$TMPDIR/keybox-update.history" ]
unset DISABLE_DURING_DOWNLOAD
echo 'ok: backend disabled during download is re-detected before installation'

fixture revoked tricky_store 246
mkdir -p "$ROOT/data/adb/tricky_store"
printf old > "$ROOT/data/adb/tricky_store/keybox.xml"
export STATUS_JSON='{"entries":{"2a":{"status":"REVOKED","reason":"KEY_COMPROMISE"}}}'
if action; then echo 'FAIL: revoked candidate installed'; exit 1; fi
[ "$(cat "$ROOT/data/adb/tricky_store/keybox.xml")" = old ]
[ ! -e "$TMPDIR/keybox-update.history" ]
grep -q 'rejecting this source and all its routes; trying other sources' "$ROOT/action.log"
[ -z "$(find "$TMPDIR" -name 'keybox-lite.*' -type d -print)" ]
echo 'ok: real revoked certificate eliminates its source, retains old keybox and cleans work files'
