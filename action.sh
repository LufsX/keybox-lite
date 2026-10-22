#!/system/bin/sh
# Keybox Lite - Action button: fetch, decode, validate and install a keybox.
#
# Downloads use curl/wget; decoding, ASN.1 serial extraction and the
# Google revocation check (status download included) run in the bundled keyboxlite binary.
#
# Source order and proxy settings.
# Duck/Tricky Addon is excluded from automatic updates; add addon to opt in.
SOURCES="meow spectre"
ACCEL="https://c.etc.moe/"

[ -n "$TMPDIR" ] || TMPDIR=/data/local/tmp

MODDIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd) || exit 1
if [ "$1" = --worker ]; then
    # Workers start fresh shells: no inherited traps, settings or lock descriptors.
    WORK=$2
    KBL_RACE=$3
    KBL_SOURCE=$4
    KBL_PREFIX=$5
    case "$KBL_PREFIX" in
        "") KBL_ROUTE=direct ;;
        https://gh-proxy.com/) KBL_ROUTE=gh-proxy ;;
        *) KBL_ROUTE=c.etc.moe ;;
    esac
    TMPDIR=$WORK
    export TMPDIR
else
KBL_LIBRARY=1
. "$MODDIR/settings.sh" || exit 1
unset KBL_LIBRARY
kbl_init && kbl_enabled || exit 1
export KB_STATUS_CACHE="$STATE/status-cache"
BIN="$MODDIR/bin/keyboxlite"
[ -x "$BIN" ] || BIN="$MODDIR/keyboxlite"
[ -x "$BIN" ] || BIN="$(command -v keyboxlite 2>/dev/null)"
[ -n "$BIN" ] && [ -x "$BIN" ] || { echo "ERROR: keyboxlite binary not found next to action.sh"; exit 1; }

if [ "$1" != --managed ]; then
    exec "$BIN" run-update "$MODDIR/action.sh" "$STATE"
fi
[ ! -L "$STATE/action.lock" ] || exit 1
exec 7>> "$STATE/action.lock" || exit 1
kbl_flock -n 7 || { echo "ERROR: another keybox update is running"; exit 1; }

LOG="$TMPDIR/keybox-update.log"
HISTORY="$TMPDIR/keybox-update.history"
WORK=
STAGED=
BAK=
HIST_TMP=
LOG_TMP=
WORKERS=
cleanup() {
    cleanup_rc=$?
    trap '' HUP INT TERM
    [ -z "$WORKERS" ] || stop_workers
    [ ! -d "$WORK/race" ] || flush_worker_logs
    [ -z "$STAGED" ] || rm -f "$STAGED"
    [ -z "$HIST_TMP" ] || rm -f "$HIST_TMP"
    [ -z "$LOG_TMP" ] || rm -f "$LOG_TMP"
    [ -z "$WORK" ] || rm -rf "$WORK"
    return "$cleanup_rc"
}
trap cleanup 0
trap 'exit 1' HUP INT TERM
WORK=$(mktemp -d "$TMPDIR/keybox-lite.XXXXXX") || exit 1
LOG_TMP=$(mktemp "$LOG.XXXXXX") || exit 1
# Keep the opened inode, so later log writes cannot follow a replaced symlink.
exec 6>> "$LOG_TMP" || exit 1
[ ! -d "$LOG" ] && mv -f "$LOG_TMP" "$LOG" || exit 1
LOG_TMP=
# Keep all temporary files for this update inside its private work directory.
TMPDIR=$WORK
export TMPDIR

# Action shells may run with a minimal PATH, so locate busybox for wget.
BB=""
for c in /data/adb/ksu/bin/busybox /data/adb/ap/bin/busybox /data/adb/magisk/busybox /system/bin/busybox /system/xbin/busybox; do
    [ -x "$c" ] && { BB="$c"; break; }
done
[ -n "$BB" ] || BB="$(command -v busybox 2>/dev/null)"
fi

. "$MODDIR/sources.sh" || exit 1

COLOR_OUTPUT=0
if [ -t 1 ] && [ "${TERM:-}" != dumb ] && [ -z "${NO_COLOR:-}" ]; then COLOR_OUTPUT=1; fi
say() {
    if [ -n "$KBL_RACE" ]; then
        printf '%s\n' "[$KBL_SOURCE / $KBL_ROUTE] $*"
    else
        color=
        if [ "$COLOR_OUTPUT" = 1 ]; then
            case "$*" in
                *"[FAIL]"*|"RESULT: revoked"*) color='1;31' ;;
                *"[WARN]"*|*"[WAIT]"*|*"[STOP]"*|"RESULT: unknown"*|"RESULT: expired"*|"RESULT: not_yet_valid"*) color='33' ;;
                *"[DONE]"*|*"[OK]"*|"RESULT: not revoked"*) color='32' ;;
                *"[CACHE]"*) color='35' ;;
                *"[CHECK]"*|*"[FETCH]"*|*"[INSTALL]"*) color='36' ;;
                "Keybox Lite "*) color='1' ;;
            esac
        fi
        if [ -n "$color" ]; then printf '\033[%sm%s\033[0m\n' "$color" "$*"; else printf '%s\n' "$*"; fi
        printf '%s | %s\n' "$(date '+%F %T')" "$*" >&6
    fi
}
die() { say "[FAIL] $*"; exit 1; }

if [ "$1" != --worker ]; then
MODULE_VERSION=$(sed -n 's/^version=//p' "$MODDIR/module.prop" 2>/dev/null)
say "Keybox Lite ${MODULE_VERSION:-unknown}"
say "[CHECK] Checking installation"

BACKEND=$("$BIN" backend id) || die "no active supported backend or invalid backend configuration"
TARGET=$("$BIN" backend target) || die "cannot resolve backend keybox path"
[ -n "$TARGET" ] || die "backend returned an empty keybox path"
say "Backend: $BACKEND"
say "Target: $TARGET"
say "[FETCH] Downloading and checking candidates"
fi

# ---------------------------------------------------------------- isolated route download
# Each worker fixes its URL route, with downloader fallback within that route.
# Revocation eliminates every route belonging to that source.
dl() (
    case "$KBL_PREFIX:$1" in
        https://gh-proxy.com/:https://github.com/*|https://gh-proxy.com/:https://raw.githubusercontent.com/*) ;;
        https://gh-proxy.com/:*) return 1 ;;
    esac
    url="$KBL_PREFIX$1"
    out=$2
    errfile="$WORK/downloader-error"
    say "[FETCH] $url"
    for KBL_DOWNLOADER in $TOOLS; do
        say "[FETCH] Downloading with $KBL_DOWNLOADER"
        rc=0
        rm -f "$out"
        case "$KBL_DOWNLOADER" in
            curl)
                curl -fsSL --connect-timeout 8 --max-time 60 --speed-time 15 --speed-limit 128 --max-filesize 1048576 -o "$out" "$url" 2>"$errfile" || rc=$?
                ;;
            busybox) "$BB" wget -q -T 20 -O "$out" "$url" 2>"$errfile" || rc=$? ;;
            wget) wget -q -T 20 -O "$out" "$url" 2>"$errfile" || rc=$? ;;
        esac
        if [ "$rc" -ne 0 ]; then
            say "[WARN] Download: $KBL_DOWNLOADER exit $rc; trying another downloader"
            # Only bounded downloader diagnostics, never the downloaded body.
            if [ -s "$errfile" ]; then
                head -c 1024 "$errfile" | head -n 3 | while IFS= read -r line; do say "  $line"; done
            fi
            continue
        fi
        [ -s "$out" ] || { say "  $KBL_DOWNLOADER returned an empty response"; continue; }
        size=$(wc -c < "$out")
        [ "$size" -le 1048576 ] || { say "  response exceeds 1 MiB"; continue; }
        return 0
    done
    rm -f "$out"
    return 1
)

# Every source candidate passes cleanup, backend rules, validity and revocation.
# Ordinary validation failures allow Spectre candidate fallback.
kbl_source_rejected() { [ -d "$KBL_RACE/revoked-$KBL_SOURCE" ]; }
kbl_validate_candidate() (
    candidate=$1
    cleaned="$candidate.prepared"
    if ! "$BIN" prepare "$BACKEND" < "$candidate" > "$cleaned"; then
        rm -f "$cleaned"
        say "[WARN] Incompatible with $BACKEND; trying next candidate."
        exit 1
    fi
    mv -f "$cleaned" "$candidate" || exit 1
    kbl_source_rejected && exit 1
    say "[CHECK] Checking certificates"
    KB_ACCEL="$ACCEL" "$BIN" verify "$candidate"
    rev=$?
    case "$rev" in
        0) say "[OK] Backend and certificate checks passed."; exit 0 ;;
        1)
            say "[FAIL] REVOKED/SUSPENDED: rejecting this source and all its routes; trying other sources."
            mkdir "$KBL_RACE/revoked-$KBL_SOURCE" 2>/dev/null ||
                [ -d "$KBL_RACE/revoked-$KBL_SOURCE" ] || {
                    # No reliable revocation record: fail the entire attempt.
                    kill -TERM "$KBL_PARENT"
                    exit 1
                } ;;
        3) say "[WARN] Expired or not yet valid (device time); trying next candidate." ;;
        *) say "[WARN] Could not verify candidate (exit $rev); trying next candidate." ;;
    esac
    exit 1
)

# Re-detect before publishing so a backend/configuration change during a long
# download cannot install to a previously selected (now inactive) destination.
check_target() {
    current_backend=$("$BIN" backend id) || return 1
    current_target=$("$BIN" backend target) || return 1
    [ "$current_backend" = "$BACKEND" ] && [ "$current_target" = "$TARGET" ]
}

# ---------------------------------------------------------------- standalone install
install_keybox() { # install_keybox <file>
    kbl_enabled || die "module is disabled or being removed"
    check_target || die "backend or keybox path changed during update; retry"
    say "[INSTALL] Installing for $BACKEND"
    [ ! -L "$TARGET" ] && [ ! -d "$TARGET" ] || die "unsafe target $TARGET"
    # Only newly created target directories use 0755; existing backend metadata
    # stays untouched. The private work/state directories retain umask 077.
    (umask 022; mkdir -p "$(dirname "$TARGET")") || die "cannot create target directory"
    STAGED=$(mktemp "$TARGET.new.XXXXXX") || die "cannot stage $TARGET"
    if [ -e "$TARGET" ]; then
        # Preserve the existing owner and mode for the keystore consumer.
        cp -p "$TARGET" "$STAGED" || die "cannot preserve target metadata"
        BAK=$(mktemp "$TARGET.bak.XXXXXX") || die "cannot create backup"
        if ! cp -p "$TARGET" "$BAK"; then
            rm -f "$BAK"
            die "cannot back up $TARGET; old keybox retained"
        fi
        say "Backed up old keybox -> $BAK"
    else
        chmod 644 "$STAGED" || die "cannot set target permissions"
    fi
    cat "$1" > "$STAGED" || die "cannot stage keybox; old keybox retained"
    kbl_enabled || die "module is disabled or being removed"
    check_target || die "backend or keybox path changed before install; old keybox retained"
    mv -f "$STAGED" "$TARGET" || die "cannot replace $TARGET; old keybox retained"
    STAGED=
    say "[DONE] Keybox installed to $TARGET."
    return 0
}

# Freeze each process before examining its children, so it cannot fork past
# cleanup. Keep workers in run-update's process group as a cancellation backstop.
# Android kernels may omit task/children; /proc/stat supplies the same parent IDs.
stop_tree() (
    kill -STOP "$1" 2>/dev/null || return 0
    children=
    if [ -r "/proc/$1/task/$1/children" ]; then
        read -r children < "/proc/$1/task/$1/children" || :
    else
        for stat in /proc/[0-9]*/stat; do
            IFS= read -r entry 2>/dev/null < "$stat" || continue
            fields=${entry##*) }
            set -- "$1" $fields
            [ "$3" = "$1" ] || continue
            child=${stat#/proc/}
            children="$children ${child%/stat}"
        done
    fi
    for child in $children; do stop_tree "$child"; done
    kill -KILL "$1" 2>/dev/null || :
)

stop_workers() {
    for worker in $WORKERS; do stop_tree "$worker"; done
    for worker in $WORKERS; do wait "$worker" 2>/dev/null || :; done
    WORKERS=
}

flush_worker_log() {
    [ -f "$1/log" ] && [ ! -f "$1/log-flushed" ] || return 0
    while IFS= read -r line; do say "$line"; done < "$1/log"
    : > "$1/log-flushed"
}

flush_worker_logs() {
    for log_dir in "$WORK/race"/[0-9]*; do flush_worker_log "$log_dir"; done
}

if [ "$1" = --worker ]; then
    # Source helpers may install their own traps; only this fresh shell parks.
    # Parking preserves the parent chain until the supervisor reaps the tree.
    if kbl_fetch_source "$KBL_SOURCE" "$WORK/result" && ! kbl_source_rejected; then
        # Retain every complete result for the supervisor's final choice.
        if printf '%s\n' "$WORK" > "$WORK/accepted.tmp"; then
            mv "$WORK/accepted.tmp" "$WORK/accepted" || :
        fi
    fi
    : > "$WORK/done" || { kill -TERM "$KBL_PARENT"; exit 1; }
    while :; do sleep 1; done
fi

# ---------------------------------------------------------------- main
# At most six active source/route workers, including manually enabled Addon.
# Source rejection and route results are private to this attempt.
KBL_PARENT=$$
export BIN BACKEND ACCEL BB KBL_PARENT
RACE="$WORK/race"
mkdir "$RACE" || die "cannot create worker directory"
TOOLS=
command -v curl >/dev/null 2>&1 && TOOLS="$TOOLS curl"
# Do not probe wget --help here: even a broken applet must not delay other tools.
[ -n "$BB" ] && TOOLS="$TOOLS busybox"
command -v wget >/dev/null 2>&1 && TOOLS="$TOOLS wget"
[ -n "$TOOLS" ] || die "no supported downloader"
export TOOLS
COUNT=0
for src in meow spectre addon; do
    case " $SOURCES " in *" $src "*) ;; *) continue ;; esac
    for route in direct accelerated github; do
        prefix=
        if [ "$route" = accelerated ]; then
            [ -n "$ACCEL" ] || continue
            prefix=$ACCEL
        fi
        # gh-proxy serves GitHub only; dl also checks catalog-provided URLs.
        [ "$route" != github ] || prefix=https://gh-proxy.com/
        case "$src:$route:${SPECTRE_CATALOG:-https://rawbin.dpejoh.com/catalog}" in
            spectre:github:https://github.com/*|spectre:github:https://raw.githubusercontent.com/*) ;;
            spectre:github:*) continue ;;
        esac
        COUNT=$((COUNT + 1))
        worker_dir="$RACE/$COUNT"
        mkdir "$worker_dir" || die "cannot create worker directory"
        printf '%s\n' "$src" > "$worker_dir/source" || die "cannot write source"
        printf '%s\n' "$prefix" > "$worker_dir/prefix" || die "cannot write route"
    done
done
choose_accepted() {
    winner=
    for accepted_dir in "$RACE"/[0-9]*; do
        [ -f "$accepted_dir/accepted" ] || continue
        IFS= read -r accepted_source < "$accepted_dir/source" || die "cannot read accepted source"
        [ ! -d "$RACE/revoked-$accepted_source" ] || continue
        winner=$accepted_dir
        break
    done
}
NEXT=1
ACTIVE=0
while :; do
    for worker_dir in "$RACE"/[0-9]*; do
        [ -f "$worker_dir/pid" ] || continue
        IFS= read -r src < "$worker_dir/source" || die "cannot read source"
        [ -f "$worker_dir/done" ] || [ -d "$RACE/revoked-$src" ] || continue
        IFS= read -r worker < "$worker_dir/pid" || die "cannot read worker PID"
        stop_tree "$worker"
        wait "$worker" 2>/dev/null || :
        rm -f "$worker_dir/pid" || die "cannot clear worker PID"
        ACTIVE=$((ACTIVE - 1))
        flush_worker_log "$worker_dir"
    done
    WORKERS=
    for worker_dir in "$RACE"/[0-9]*; do
        [ -f "$worker_dir/pid" ] || continue
        IFS= read -r worker < "$worker_dir/pid" || die "cannot read worker PID"
        WORKERS="$WORKERS $worker"
    done
    choose_accepted
    if [ -n "$winner" ]; then
        # Stop only the selected source first; other sources keep their results
        # and can continue if a sibling reports revocation during shutdown.
        IFS= read -r selected_source < "$winner/source" || die "cannot read selected source"
        for worker_dir in "$RACE"/[0-9]*; do
            IFS= read -r src < "$worker_dir/source" || die "cannot read source"
            [ "$src" = "$selected_source" ] && [ -f "$worker_dir/pid" ] || continue
            IFS= read -r worker < "$worker_dir/pid" || die "cannot read worker PID"
            stop_tree "$worker"
            wait "$worker" 2>/dev/null || :
            rm -f "$worker_dir/pid" || die "cannot clear worker PID"
            ACTIVE=$((ACTIVE - 1))
        done
        [ -d "$RACE/revoked-$selected_source" ] || break
        continue
    fi
    while [ "$ACTIVE" -lt 6 ] && [ "$NEXT" -le "$COUNT" ]; do
        worker_dir="$RACE/$NEXT"
        IFS= read -r src < "$worker_dir/source" || die "cannot read source"
        NEXT=$((NEXT + 1))
        [ ! -d "$RACE/revoked-$src" ] || continue
        IFS= read -r prefix < "$worker_dir/prefix" || die "cannot read route"
        case "$prefix" in
            "") route=direct ;;
            https://gh-proxy.com/) route=gh-proxy ;;
            *) route=c.etc.moe ;;
        esac
        say "[FETCH] [$src / $route] Starting"
        # Close inherited action/config/scheduler locks explicitly.
        sh "$MODDIR/action.sh" --worker "$worker_dir" "$RACE" "$src" "$prefix" > "$worker_dir/log" 2>&1 6>&- 7>&- 8>&- 9>&- &
        worker=$!
        WORKERS="$WORKERS $worker"
        printf '%s\n' "$worker" > "$worker_dir/pid" || die "cannot write worker PID"
        ACTIVE=$((ACTIVE + 1))
    done
    [ "$ACTIVE" -gt 0 ] || break
    sleep 0.1
done
stop_workers
flush_worker_logs
choose_accepted
[ -n "$winner" ] || die "all sources failed (network, backend compatibility or certificate checks); old keybox retained"
OUT="$winner/result"
IFS= read -r src < "$winner/source" || die "cannot read winning source"
say "[OK] [$src] Passed backend and certificate checks ($(wc -c < "$OUT" | tr -d ' ') bytes)"
install_keybox "$OUT"
HIST_TMP=$(mktemp "$HISTORY.XXXXXX") || { say "WARNING: installed, but cannot record history"; exit 0; }
if { if [ -f "$HISTORY" ] && [ ! -L "$HISTORY" ]; then cat "$HISTORY"; fi
    printf '%s | %s | %s bytes | installed\n' "$(date '+%F %T')" "$src" "$(wc -c < "$OUT" | tr -d ' ')"
} > "$HIST_TMP" && [ ! -d "$HISTORY" ] && mv -f "$HIST_TMP" "$HISTORY"; then
    HIST_TMP=
else
    say "WARNING: installed, but cannot record history"
fi
exit 0
