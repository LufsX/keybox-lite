#!/bin/sh
# Isolated action supervisor tests; all payloads and commands are synthetic.
set -eu
REPO=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
BOX=$(mktemp -d)
job=
cleanup() {
    [ -z "$job" ] || { kill -TERM "$job" 2>/dev/null || :; wait "$job" 2>/dev/null || :; }
    rm -rf "$BOX"
}
trap cleanup 0
trap 'exit 1' HUP INT TERM
fail() { echo "FAIL: $*" >&2; [ ! -f "$ROOT/log" ] || cat "$ROOT/log" >&2; exit 1; }
export BOX
REAL_MV=$(command -v mv)
REAL_SLEEP=$(command -v sleep)
REAL_MKDIR=$(command -v mkdir)
export REAL_MV REAL_SLEEP REAL_MKDIR
mkdir -p "$BOX/mock"
cat > "$BOX/mock/curl" <<'MOCK'
#!/bin/sh
out=
for arg do
    if [ "${prev:-}" = -o ]; then out=$arg; fi
    prev=$arg
    url=$arg
done
case "$url" in
    https://gh-proxy.com/https://rawbin*) echo wrong > "$ROOT/wrong-proxy"; exit 99 ;;
    https://gh-proxy.com/*) route=github ;;
    https://c.etc.moe/*) route=accel ;;
    *) route=direct ;;
esac
printf '%s\n' "$url" >> "$ROOT/requests"
case "$url" in *Megatron) source=meow ;; *) source=spectre ;; esac
: > "$ROOT/$source-$route"
stall() {
    sleep 120 &
    echo $! >> "$ROOT/children"
    wait
}
case "$MODE:$source:$route" in
    github:meow:direct|spectre:meow:*) stall ;;
    prepublished:meow:direct)
        while [ "$(find "$ROOT/tmp" -name accepted | wc -l)" -lt 2 ]; do sleep 0.05; done
        printf REVOKED > "$out" ;;
    prepublished:meow:accel) printf GOOD > "$out" ;;
    prepublished:spectre:direct)
        case "$url" in */catalog) printf '%s\n' GOOD > "$out" ;; *) printf GOOD > "$out" ;; esac ;;
    revoked:meow:direct|allfail:meow:direct|markerfail:meow:direct) printf REVOKED > "$out" ;;
    revoked:meow:*|allfail:meow:*)
        while [ ! -e "$ROOT/revoked" ]; do sleep 0.05; done
        printf GOOD > "$out" ;;
    revoked:spectre:direct|shutdown:spectre:direct|retained:spectre:direct)
        case "$url" in
            */catalog)
                while [ ! -e "$ROOT/revoked" ]; do sleep 0.05; done
                printf '%s\n' GOOD > "$out" ;;
            *) printf GOOD > "$out" ;;
        esac ;;
    shutdown:meow:direct|retained:meow:direct) printf GOOD > "$out" ;;
    shutdown:meow:*|retained:meow:*) stall ;;
    spectre-revoked:spectre:direct)
        case "$url" in
            */catalog) printf '%s\n' REVOKED GOOD > "$out" ;;
            *) printf '%s' "${url##*/}" > "$out" ;;
        esac ;;
    github:meow:accel) printf BAD_DECODE > "$out" ;;
    github:meow:github)
        while [ ! -e "$ROOT/meow-direct" ] || [ ! -e "$ROOT/meow-accel" ]; do sleep 0.05; done
        printf GOOD > "$out" ;;
    cancelverify:*|publication:meow:direct) printf GOOD > "$out" ;;
    publication:meow:github)
        while [ ! -e "$ROOT/publication-failed" ]; do sleep 0.05; done
        printf GOOD > "$out" ;;
    badfast:meow:accel) printf BAD_PREPARE > "$out" ;;
    badfast:meow:github) printf UNKNOWN > "$out" ;;
    badfast:meow:direct)
        while [ ! -e "$ROOT/rejected-prepare" ] || [ ! -e "$ROOT/rejected-verify" ]; do sleep 0.05; done
        printf GOOD > "$out" ;;
    spectre:spectre:direct)
        case "$url" in
            */catalog) printf '%s\n' BAD_PREPARE UNKNOWN TIME GOOD > "$out" ;;
            *) printf '%s' "${url##*/}" > "$out" ;;
        esac ;;
    cap:*)
        while [ ! -e "$ROOT/release" ]; do sleep 0.05; done
        exit 22 ;;
    *) echo synthetic-downloader-error >&2; exit 22 ;;
esac
MOCK
cat > "$BOX/mock/mkdir" <<'MOCK'
#!/bin/sh
case "$MODE:$*" in markerfail:*/revoked-meow) exit 1 ;; esac
exec "$REAL_MKDIR" "$@"
MOCK
cat > "$BOX/mock/sleep" <<'MOCK'
#!/bin/sh
if [ "$MODE" = prepublished ] && [ "$1" = 0.1 ]; then
    while [ -z "$(find "$ROOT/tmp" -type d -name revoked-meow)" ]; do "$REAL_SLEEP" 0.05; done
fi
exec "$REAL_SLEEP" "$@"
MOCK
cat > "$BOX/mock/mv" <<'MOCK'
#!/bin/sh
case "$*" in
    *accepted*)
        if [ "$MODE" = publication ] && mkdir "$ROOT/first-publication" 2>/dev/null; then
            : > "$ROOT/publication-failed"
            exit 1
        fi ;;
esac
exec "$REAL_MV" "$@"
MOCK
cat > "$BOX/mock/busybox" <<'MOCK'
#!/bin/sh
[ "$MODE" = fallback ] || exit 1
[ "$1" = wget ] || exit 1
shift
for arg do
    if [ "${prev:-}" = -O ]; then out=$arg; fi
    prev=$arg
done
printf GOOD > "$out"
: > "$ROOT/used-busybox"
MOCK
printf '#!/bin/sh\nexit 1\n' > "$BOX/mock/wget"
chmod +x "$BOX/mock/"*
export PATH="$BOX/mock:$PATH"
fixture() {
    MODE=$1
    ROOT="$BOX/$MODE"
    export MODE ROOT
    mkdir -p "$ROOT/mod/bin" "$ROOT/tmp" "$ROOT/state"
    cp "$REPO/action.sh" "$REPO/sources.sh" "$ROOT/mod/"
    cat > "$ROOT/mod/settings.sh" <<'MOCK'
kbl_init() { STATE="$ROOT/state"; umask 077; }
kbl_enabled() { return 0; }
kbl_flock() { flock -n 0 <&7; }
MOCK
    cat > "$ROOT/mod/bin/keyboxlite" <<'MOCK'
#!/bin/sh
case "$1" in
    backend) if [ "$2" = id ]; then echo tricky-store; else echo "$ROOT/installed"; fi ;;
    decode-*) data=$(cat); [ "$data" != BAD_DECODE ] || exit 1; printf '%s' "$data" ;;
    spectre-urls) sed 's|^|https://rawbin.dpejoh.com/key/|' "$2" ;;
    prepare)
        data=$(cat)
        printf '%s\n' "$data" >> "$ROOT/prepared"
        [ "$data" != BAD_PREPARE ] || { : > "$ROOT/rejected-prepare"; exit 1; }
        printf '%s' "$data" ;;
    verify)
        [ "$KB_STATUS_CACHE" = "$ROOT/state/status-cache" ] || exit 99
        if [ "$MODE" = cancelverify ]; then
            sleep 120 &
            echo $! >> "$ROOT/children"
            : > "$ROOT/verify-started"
            wait
        fi
        data=$(cat "$2")
        printf '%s\n' "$data" >> "$ROOT/verified"
        case "$data" in
            GOOD) exit 0 ;; REVOKED) : > "$ROOT/revoked"; exit 1 ;; TIME) exit 3 ;;
            *) : > "$ROOT/rejected-verify"; exit 2 ;;
        esac ;;
    *) exit 99 ;;
esac
MOCK
    chmod +x "$ROOT/mod/bin/keyboxlite"
    printf OLD > "$ROOT/installed"
    TMPDIR="$ROOT/tmp"
    export TMPDIR
}
assert_clean() {
    [ -z "$(find "$ROOT/tmp" -type d -name 'keybox-lite.*' -print)" ] || fail 'work directories leaked'
    [ ! -e "$ROOT/wrong-proxy" ] || fail 'non-GitHub URL sent to gh-proxy'
    if [ -f "$ROOT/children" ]; then
        for child in $(cat "$ROOT/children"); do
            if [ -f "/proc/$child/stat" ] && ! grep -q ') Z ' "/proc/$child/stat"; then fail 'losing downloader descendant survived'; fi
        done
    fi
    flock -n "$ROOT/state/action.lock" true || fail 'worker retained action lock'
}
for mode in github badfast spectre fallback publication revoked shutdown retained prepublished; do
    fixture "$mode"
    case "$mode" in
        shutdown|retained)
            # Make a revocation arrive exactly while the chosen source stops.
            # In retained mode the other success is already published too.
            sed '/^    kill -STOP "\$1"/i\
    if [ ! -e "$ROOT/revoked" ] && [ -n "${selected_source:-}" ]; then\
        mkdir "$RACE/revoked-meow" || exit 1\
        : > "$ROOT/revoked"\
        if [ "$MODE" = retained ]; then\
            while [ ! -f "$RACE/4/accepted" ]; do sleep 0.05; done\
        fi\
    fi' "$ROOT/mod/action.sh" > "$ROOT/mod/new"
            mv "$ROOT/mod/new" "$ROOT/mod/action.sh"
            ;;
    esac
    sh "$ROOT/mod/action.sh" --managed > "$ROOT/log" 2>&1 &
    job=$!
    # A bounded watchdog turns a serial-download regression into a prompt failure.
    (
        sleep 15 &
        timer=$!
        trap 'kill "$timer" 2>/dev/null || :; wait "$timer" 2>/dev/null || :; exit 0' HUP INT TERM
        wait "$timer"
        kill -TERM "$job" 2>/dev/null || :
    ) &
    watchdog=$!
    rc=0
    wait "$job" || rc=$?
    job=
    kill "$watchdog" 2>/dev/null || :
    wait "$watchdog" 2>/dev/null || :
    [ "$rc" = 0 ] || fail "$mode update failed or stalled"
    [ "$(cat "$ROOT/installed")" = GOOD ] || fail 'unvalidated response installed'
    [ "$(wc -l < "$ROOT/tmp/keybox-update.history")" -eq 1 ] || fail 'multiple installs'
    case "$mode" in
        spectre)
            [ "$(cat "$ROOT/verified")" = 'UNKNOWN
TIME
GOOD' ] || fail 'Spectre validation fallback order' ;;
        fallback)
            [ -e "$ROOT/used-busybox" ] || fail 'curl failure skipped BusyBox'
            grep -q synthetic-downloader-error "$ROOT/log" || fail 'download diagnostics missing' ;;
        publication) [ -e "$ROOT/publication-failed" ] || fail 'publication failure was not exercised' ;;
        revoked|shutdown|retained|prepublished)
            grep -q 'spectre.*installed$' "$ROOT/tmp/keybox-update.history" || fail 'eliminated Meow installed' ;;
    esac
    assert_clean
    echo "ok: source/route race $mode, one verified install and descendant/lock cleanup"
done
for mode in spectre-revoked allfail markerfail; do
    fixture "$mode"
    if sh "$ROOT/mod/action.sh" --managed > "$ROOT/log" 2>&1; then
        fail "$mode installed an eliminated source"
    fi
    [ "$(cat "$ROOT/installed")" = OLD ] || fail "$mode replaced old output"
    [ ! -e "$ROOT/tmp/keybox-update.history" ] || fail "$mode recorded installation"
    if [ "$mode" = spectre-revoked ]; then
        [ "$(cat "$ROOT/verified")" = REVOKED ] || fail 'Spectre continued after revocation'
        ! grep -q '/key/GOOD' "$ROOT/requests" || fail 'Spectre downloaded a later candidate'
    fi
    assert_clean
    echo "ok: $mode eliminates source and retains old keybox"
done
fixture cancelverify
sh "$ROOT/mod/action.sh" --managed > "$ROOT/log" 2>&1 &
job=$!
tries=0
while [ ! -f "$ROOT/verify-started" ]; do
    tries=$((tries + 1)); [ "$tries" -lt 100 ] || fail "verification never started"
    sleep 0.05
done
kill -TERM "$job"
if wait "$job"; then fail "cancelled verification installed"; fi
job=
[ "$(cat "$ROOT/installed")" = OLD ] || fail "cancellation replaced old output"
[ ! -e "$ROOT/tmp/keybox-update.history" ] || fail "cancellation recorded installation"
grep -q "Downloading with curl" "$ROOT/log" || fail "cancellation lost worker diagnostics"
assert_clean
echo "ok: cancellation during verify kills descendants, flushes logs and retains old keybox"
fixture cap
sed 's/^SOURCES="meow spectre"$/SOURCES="meow spectre addon"/' "$ROOT/mod/action.sh" > "$ROOT/mod/new"
mv "$ROOT/mod/new" "$ROOT/mod/action.sh"
sh "$ROOT/mod/action.sh" --managed > "$ROOT/log" 2>&1 &
job=$!
tries=0
while [ ! -f "$ROOT/requests" ] || [ "$(wc -l < "$ROOT/requests")" -lt 6 ]; do
    tries=$((tries + 1)); [ "$tries" -lt 100 ] || fail 'six workers never started'
    sleep 0.05
done
sleep 0.2
[ "$(wc -l < "$ROOT/requests")" -eq 6 ] || fail 'worker limit exceeded'
: > "$ROOT/release"
if wait "$job"; then fail 'failed queued candidates installed'; fi
job=
[ "$(wc -l < "$ROOT/requests")" -eq 8 ] || fail 'queued Addon routes were skipped'
[ "$(cat "$ROOT/installed")" = OLD ] || fail 'failed workers replaced old output'
assert_clean
echo 'ok: hard limit of six active workers; queued routes continue after failure'
