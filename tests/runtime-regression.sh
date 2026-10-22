#!/bin/sh
# Host-only sandbox tests. No production /data paths or network are used.
set -eu
REPO=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SANDBOX=$(mktemp -d)
REAL_CP=$(command -v cp)
REAL_MV=$(command -v mv)
REAL_SLEEP=$(command -v sleep)
HOST_PATH=$PATH
export REAL_CP REAL_MV REAL_SLEEP
JOBS=
cleanup() {
    for job in $JOBS; do kill -TERM "$job" 2>/dev/null || :; done
    for job in $JOBS; do wait "$job" 2>/dev/null || :; done
    rm -rf "$SANDBOX"
}
trap cleanup 0
trap 'exit 1' HUP INT TERM
fail() { echo "FAIL: $*" >&2; exit 1; }
ok() { echo "ok: $*"; }
wait_file() {
    tries=0
    until [ -e "$1" ]; do
        tries=$((tries + 1))
        [ "$tries" -lt 200 ] || fail "timed out waiting for $1"
        "$REAL_SLEEP" 0.05
    done
}
fixture() {
    TEST_ROOT="$SANDBOX/$1"
    export TEST_ROOT
    mkdir -p "$TEST_ROOT/mod/bin" "$TEST_ROOT/mock" "$TEST_ROOT/data/adb/modules/tricky_store" \
        "$TEST_ROOT/data/adb/tricky_store" "$TEST_ROOT/data/local/tmp"
    for script in action.sh sources.sh settings.sh service.sh uninstall.sh customize.sh; do
        # Only sandbox copies are rewritten; production scripts are never run.
        sed "s|/data/|$TEST_ROOT/data/|g" "$REPO/$script" > "$TEST_ROOT/mod/$script"
    done
    TARGET="$TEST_ROOT/data/adb/tricky_store/keybox.xml"
    CONF="$TEST_ROOT/data/adb/keybox-lite.conf"
    STATE="$TEST_ROOT/data/adb/keybox-lite"
    printf '%s\n' 'old installed keybox' > "$TARGET"
    chmod 600 "$TARGET"
    "$REAL_CP" "$TARGET" "$TEST_ROOT/expected-old"
    printf 'SCHEDULE=off\nLAST_RUN=0\n' > "$CONF"
    cat > "$TEST_ROOT/mod/bin/keyboxlite" <<'MOCK'
#!/bin/sh
case "$1" in
    run-update) exec sh "$2" --managed ;;
    backend)
        [ "${BACKEND_RC:-0}" = 0 ] || exit "$BACKEND_RC"
        if [ "$2" = id ]; then echo tricky-store; else echo "$TEST_ROOT/data/adb/tricky_store/keybox.xml"; fi
        ;;
    prepare)
        cat
        exit "${PREPARE_RC:-0}"
        ;;
    decode-meow|decode-spectre|decode-addon)
        : > "$TEST_ROOT/decoded"
        cat
        exit "${DECODE_RC:-0}"
        ;;
    spectre-url|spectre-urls) echo 'https://example.invalid/synthetic' ;;
    verify)
        : > "$TEST_ROOT/verified"
        case "$TMPDIR" in "$TEST_ROOT"/data/local/tmp/keybox-lite.*) ;; *) exit 99 ;; esac
        exit "${VERIFY_RC:-0}"
        ;;
    *) exit 99 ;;
esac
MOCK
    cat > "$TEST_ROOT/mock/curl" <<'MOCK'
#!/bin/sh
printf 'download\n' >> "$TEST_ROOT/downloads"
[ "${SECOND_ACTION:-0}" != 1 ] || : > "$TEST_ROOT/second-downloaded"
while [ "$#" -gt 0 ]; do
    if [ "$1" = -o ]; then shift; out=$1; fi
    shift
done
if [ "${CURL_PAUSE:-0}" = 1 ]; then
    : > "$TEST_ROOT/download-entered"
    while [ ! -e "$TEST_ROOT/release-download" ]; do "$REAL_SLEEP" 0.05; done
fi
printf '<AndroidAttestation>synthetic test data</AndroidAttestation>\n' > "$out"
exit "${CURL_RC:-0}"
MOCK
    # No real downloader may escape the sandbox when curl fails.
    for downloader in wget busybox; do
        printf '#!/bin/sh\nexit 1\n' > "$TEST_ROOT/mock/$downloader"
    done
    cat > "$TEST_ROOT/mock/cp" <<'MOCK'
#!/bin/sh
if [ "${FAIL_BACKUP:-0}" = 1 ]; then
    for arg do case "$arg" in *.bak.*) exit 1 ;; esac; done
fi
exec "$REAL_CP" "$@"
MOCK
    cat > "$TEST_ROOT/mock/mv" <<'MOCK'
#!/bin/sh
for arg do
    case "$arg" in
        *.new.*) [ "${FAIL_REPLACE:-0}" != 1 ] || exit 1 ;;
        */keybox-lite.conf.*) [ "${FAIL_CONF_MV:-0}" != 1 ] || exit 1 ;;
    esac
done
exec "$REAL_MV" "$@"
MOCK
    cat > "$TEST_ROOT/mock/sleep" <<'MOCK'
#!/bin/sh
if [ "$1" = 1800 ]; then
    : > "$TEST_ROOT/idle"
    exec "$REAL_SLEEP" 60
fi
exec "$REAL_SLEEP" "$@"
MOCK
    chmod +x "$TEST_ROOT/mod/bin/keyboxlite" "$TEST_ROOT/mock/"*
    PATH="$TEST_ROOT/mock:$HOST_PATH"
    TMPDIR="$TEST_ROOT/data/local/tmp"
    CURL_RC=0 VERIFY_RC=0 DECODE_RC=0 CURL_PAUSE=0 FAIL_BACKUP=0 FAIL_REPLACE=0 FAIL_CONF_MV=0
    PREPARE_RC=0 BACKEND_RC=0
    export PREPARE_RC BACKEND_RC
    export PATH TMPDIR CURL_RC VERIFY_RC DECODE_RC CURL_PAUSE FAIL_BACKUP FAIL_REPLACE FAIL_CONF_MV
}
action() { sh "$TEST_ROOT/mod/action.sh" > "$TEST_ROOT/action-output" 2>&1; }
unchanged() { cmp -s "$TARGET" "$TEST_ROOT/expected-old" || fail 'installed keybox changed'; }
no_staging() {
    for tmp in "$TARGET".new.* "$TMPDIR"/keybox-lite.*; do
        [ ! -e "$tmp" ] || fail "temporary file leaked: $tmp"
    done
}

fixture downloader-fallback
CURL_RC=22
cat > "$TEST_ROOT/mock/busybox" <<'MOCK'
#!/bin/sh
[ "$1" = wget ] || exit 1
shift
[ "$1" != --help ] || exit 0
while [ "$#" -gt 0 ]; do
    if [ "$1" = -O ]; then shift; out=$1; fi
    shift
done
printf '<AndroidAttestation>synthetic test data</AndroidAttestation>\n' > "$out"
: > "$TEST_ROOT/used-busybox"
MOCK
chmod +x "$TEST_ROOT/mock/busybox"
action || fail 'curl failure did not fall back to BusyBox wget'
[ -e "$TEST_ROOT/used-busybox" ] || fail 'fallback downloader not invoked'
grep -q 'Downloading with busybox' "$TEST_ROOT/action-output" || fail 'downloader selection not logged'
grep -q 'curl exit 22' "$TEST_ROOT/action-output" || fail 'curl failure not logged'
ok 'curl failure falls back to BusyBox with diagnostic output'

fixture partial-download
CURL_RC=22
if action; then fail 'partial failed download accepted'; fi
unchanged
[ ! -e "$TEST_ROOT/decoded" ] || fail 'failed download reached decoder'
no_staging
ok 'partial download with nonzero exit cannot reach install'

for rc in 1 2 3 7; do
    fixture "verification-$rc"
    VERIFY_RC=$rc
    if action; then fail "verify exit $rc accepted"; fi
    unchanged
    [ -e "$TEST_ROOT/verified" ] || fail 'verification did not run'
    [ ! -e "$TMPDIR/keybox-update.history" ] || fail 'failed verification recorded as installed'
    no_staging
    ok "verification exit $rc retains old keybox"
done

fixture decode-failure
DECODE_RC=5
if action; then fail 'failed decoder accepted'; fi
unchanged
no_staging
ok 'decoder failure retains old keybox'

fixture prepare-failure
PREPARE_RC=2
if action; then fail 'backend-incompatible keybox accepted'; fi
unchanged
[ ! -e "$TEST_ROOT/verified" ] || fail 'prepare failure reached verification'
no_staging
ok 'backend rules reject incompatible keybox before verification'

fixture inactive-backend
BACKEND_RC=1
if action; then fail 'missing/inactive backend accepted'; fi
unchanged
[ ! -e "$TEST_ROOT/downloads" ] || fail 'invalid backend started download'
ok 'missing/inactive backend aborts before downloads'

fixture backup-failure
FAIL_BACKUP=1
if action; then fail 'backup failure ignored'; fi
unchanged
no_staging
ok 'backup failure aborts replacement'

fixture replacement-failure
FAIL_REPLACE=1
if action; then fail 'replacement failure ignored'; fi
unchanged
no_staging
ok 'rename failure leaves complete old keybox'

fixture success
# A Duck installation must not be invoked by the standalone path.
mkdir -p "$TEST_ROOT/data/adb/modules/duck-toolbox/bin"
printf '#!/bin/sh\n: > "$TEST_ROOT/duck-called"\nexit 1\n' > "$TEST_ROOT/data/adb/modules/duck-toolbox/bin/duckctl.sh"
chmod +x "$TEST_ROOT/data/adb/modules/duck-toolbox/bin/duckctl.sh"
action || fail 'valid synthetic update failed'
grep -q 'synthetic test data' "$TARGET" || fail 'new keybox missing'
[ ! -e "$TEST_ROOT/duck-called" ] || fail 'standalone install called Duck'
[ "$(stat -c %a "$TARGET")" = 600 ] || fail 'existing permissions lost'
backups=0
for backup in "$TARGET".bak.*; do
    cmp -s "$backup" "$TEST_ROOT/expected-old" || fail 'backup differs from old keybox'
    backups=$((backups + 1))
done
[ "$backups" -eq 1 ] || fail 'expected one backup'
grep -q 'installed$' "$TMPDIR/keybox-update.history" || fail 'success history missing'
no_staging
ok 'successful replacement preserves permissions and complete backup'

fixture action-contention
CURL_PAUSE=1
action &
first=$!
JOBS="$JOBS $first"
wait_file "$TEST_ROOT/download-entered"
if SECOND_ACTION=1 sh "$TEST_ROOT/mod/action.sh" > "$TEST_ROOT/second-output" 2>&1; then fail 'concurrent action accepted'; fi
grep -q 'another keybox update is running' "$TEST_ROOT/second-output" || fail 'missing contention error'
unchanged
[ ! -e "$TEST_ROOT/second-downloaded" ] || fail 'second action started downloading'
: > "$TEST_ROOT/release-download"
wait "$first" || fail 'lock owner failed'
JOBS=
ok 'action lock rejects concurrent update before download or log replacement'

fixture config-data
printf 'SCHEDULE=weekly\nLAST_RUN=0009\ntouch %s/injected\n' "$TEST_ROOT" > "$CONF"
sh "$TEST_ROOT/mod/settings.sh" get > "$TEST_ROOT/settings-output"
[ ! -e "$TEST_ROOT/injected" ] || fail 'configuration was executed'
grep -q '^LAST_RUN=9$' "$TEST_ROOT/settings-output" || fail 'timestamp normalization failed'
printf 'SCHEDULE=invalid\nLAST_RUN=999999999999999999999\n' > "$CONF"
sh "$TEST_ROOT/mod/settings.sh" get > "$TEST_ROOT/settings-output"
grep -q '^SCHEDULE=off$' "$TEST_ROOT/settings-output" || fail 'invalid schedule accepted'
grep -q '^LAST_RUN=0$' "$TEST_ROOT/settings-output" || fail 'invalid timestamp accepted'
ok 'configuration is parsed as data and invalid values are normalized'

fixture config-atomic
"$REAL_CP" "$CONF" "$TEST_ROOT/old-conf"
FAIL_CONF_MV=1
if sh "$TEST_ROOT/mod/settings.sh" set off > "$TEST_ROOT/settings-output" 2>&1; then fail 'config rename failure ignored'; fi
cmp -s "$CONF" "$TEST_ROOT/old-conf" || fail 'failed save polluted config'
for tmp in "$CONF".*; do [ ! -e "$tmp" ] || fail 'config temporary file leaked'; done
ok 'failed configuration save reports failure and retains old configuration'

fixture config-contention
mkdir -p "$STATE"
exec 8>> "$STATE/config.lock"
flock -n 0 <&8
sh "$TEST_ROOT/mod/settings.sh" set off > "$TEST_ROOT/settings-output" 2>&1 &
setter=$!
JOBS="$setter"
"$REAL_SLEEP" 0.15
kill -0 "$setter" || fail 'setter did not wait for config lock'
[ ! -s "$TEST_ROOT/settings-output" ] || fail 'setter reported success before lock release'
flock -u 0 <&8
exec 8>&-
wait "$setter" || fail 'setter failed after lock release'
JOBS=
ok 'configuration writers wait for the shared lock'

fixture scheduler-failure
printf 'SCHEDULE=daily\nLAST_RUN=0\n' > "$CONF"
VERIFY_RC=2
sh "$TEST_ROOT/mod/service.sh" --worker > "$TEST_ROOT/service-output" 2>&1 &
worker=$!
JOBS="$worker"
wait_file "$TEST_ROOT/idle"
grep -q '^LAST_RUN=0$' "$CONF" || fail 'failed update advanced LAST_RUN'
unchanged
downloads_before=$(wc -l < "$TEST_ROOT/downloads")
sh "$TEST_ROOT/mod/service.sh" --worker || fail 'duplicate scheduler returned error'
[ "$(wc -l < "$TEST_ROOT/downloads")" -eq "$downloads_before" ] || fail 'duplicate scheduler downloaded'
kill -TERM "$worker"
wait "$worker" || :
JOBS=
[ ! -e "$STATE/scheduler.pid" ] || fail 'scheduler PID not cleaned'
ok 'one scheduler only; unavailable status leaves LAST_RUN and installed data unchanged'

fixture scheduler-settings-race
printf 'SCHEDULE=daily\nLAST_RUN=0\n' > "$CONF"
CURL_PAUSE=1
sh "$TEST_ROOT/mod/service.sh" --worker > "$TEST_ROOT/service-output" 2>&1 &
worker=$!
JOBS="$worker"
wait_file "$TEST_ROOT/download-entered"
sh "$TEST_ROOT/mod/settings.sh" set weekly > "$TEST_ROOT/settings-output"
: > "$TEST_ROOT/release-download"
wait_file "$TEST_ROOT/idle"
grep -q '^SCHEDULE=weekly$' "$CONF" || fail 'scheduler overwrote user schedule'
if grep -q '^LAST_RUN=0$' "$CONF"; then fail 'successful scheduler did not record completion'; fi
sh "$TEST_ROOT/mod/uninstall.sh" || fail 'uninstall failed'
wait "$worker" || :
JOBS=
[ ! -e "$CONF" ] || fail 'uninstall retained config'
[ -e "$STATE/stopping" ] || fail 'uninstall did not block late writers'
[ ! -e "$STATE/scheduler.pid" ] || fail 'uninstall left scheduler running'
if action; then fail 'update ran after uninstall'; fi
ok 'scheduler preserves changed schedule; uninstall stops worker and blocks updates'

fixture uninstall-inflight
CURL_PAUSE=1
action &
first=$!
JOBS="$first"
wait_file "$TEST_ROOT/download-entered"
sh "$TEST_ROOT/mod/uninstall.sh" > "$TEST_ROOT/uninstall-output" 2>&1 &
uninstaller=$!
JOBS="$JOBS $uninstaller"
wait_file "$STATE/stopping"
: > "$TEST_ROOT/release-download"
if wait "$first"; then fail 'in-flight update installed after uninstall began'; fi
wait "$uninstaller" || fail 'uninstall did not finish after update released lock'
JOBS=
unchanged
[ ! -e "$CONF" ] || fail 'uninstall did not remove config'
ok 'uninstall serializes with in-flight action and retains installed data'

fixture missing-flock
mkdir "$TEST_ROOT/no-flock"
for cmd in dirname mkdir chmod; do
    ln -s "$(command -v "$cmd")" "$TEST_ROOT/no-flock/$cmd"
done
if PATH="$TEST_ROOT/no-flock" /bin/sh "$TEST_ROOT/mod/settings.sh" get > "$TEST_ROOT/settings-output" 2>&1; then
    fail 'missing flock was ignored'
fi
grep -q 'flock.*required' "$TEST_ROOT/settings-output" || fail 'missing flock diagnostic'
ok 'missing flock fails explicitly before runtime work'

fixture installer-validation
rm "$TEST_ROOT/mod/bin/keyboxlite"
if MODPATH="$TEST_ROOT/mod" sh -c '
    ui_print() { :; }
    set_perm() { :; }
    abort() { exit 1; }
    . "$MODPATH/customize.sh"
'; then fail 'installer accepted missing binary'; fi
ok 'installer rejects incomplete module without any device installation'

echo 'All isolated shell runtime regressions passed.'
