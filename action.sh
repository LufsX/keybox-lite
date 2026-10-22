#!/system/bin/sh
# Keybox Lite - Action button: fetch, decode, validate and install a keybox.
#
# Downloads use curl/wget (DNS+TLS solved); decoding, ASN.1 serial extraction and the
# Google revocation check (status download included) run in the bundled keyboxlite binary.
#
# Only knobs; edit here.
# Duck/Tricky Addon default .extra currently returns an empty body; opt-in only.
SOURCES="meow spectre"
ACCEL="https://c.etc.moe/"

[ -n "$TMPDIR" ] || TMPDIR=/data/local/tmp

MODDIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd) || exit 1
KBL_LIBRARY=1
. "$MODDIR/settings.sh" || exit 1
unset KBL_LIBRARY
kbl_init && kbl_enabled || exit 1
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
cleanup() {
    [ -z "$STAGED" ] || rm -f "$STAGED"
    [ -z "$HIST_TMP" ] || rm -f "$HIST_TMP"
    [ -z "$LOG_TMP" ] || rm -f "$LOG_TMP"
    [ -z "$WORK" ] || rm -rf "$WORK"
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

. "$MODDIR/sources.sh" || exit 1

# Action shells may run with a minimal PATH, so locate busybox for wget.
BB=""
for c in /data/adb/ksu/bin/busybox /data/adb/ap/bin/busybox /data/adb/magisk/busybox /system/bin/busybox /system/xbin/busybox; do
    [ -x "$c" ] && { BB="$c"; break; }
done
[ -n "$BB" ] || BB="$(command -v busybox 2>/dev/null)"

say() { echo "$*"; printf '%s | %s\n' "$(date '+%F %T')" "$*" >&6; }
die() { say "ERROR: $*"; exit 1; }

say "==========================================="
MODULE_VERSION=$(sed -n 's/^version=//p' "$MODDIR/module.prop" 2>/dev/null)
say " Keybox Lite ${MODULE_VERSION:-unknown} | sources: $SOURCES"
say " script: $MODDIR/action.sh (standalone-v2)"
say " accel:        $ACCEL"
say " busybox:      $BB"
say "==========================================="

BACKEND=$("$BIN" backend id) || die "no active supported backend or invalid backend configuration"
TARGET=$("$BIN" backend target) || die "cannot resolve backend keybox path"
[ -n "$TARGET" ] || die "backend returned an empty keybox path"
say " backend: $BACKEND"
say " target:  $TARGET"

# ---------------------------------------------------------------- download (direct first, proxy fallback)
candidates() { # candidates <url>
    u="$1"
    printf '%s\n' "$u"
    if [ -n "$ACCEL" ]; then
        for p in $ACCEL; do printf '%s%s\n' "$p" "$u"; done
    fi
}

dl_one() ( # Isolate attempt state from the source/candidate loops.
    say "  download: $1"
    attempted=0
    for downloader in curl busybox wget; do
        case "$downloader" in
            curl) command -v curl >/dev/null 2>&1 || continue ;;
            busybox) [ -n "$BB" ] && "$BB" wget --help >/dev/null 2>&1 || continue ;;
            wget) command -v wget >/dev/null 2>&1 || continue ;;
        esac
        attempted=1
        rm -f "$2"
        errfile="$WORK/downloader-error"
        say "  downloader: $downloader"
        rc=0
        http=
        case "$downloader" in
            curl)
                http=$(curl -fsSL --connect-timeout 8 --max-time 60 --speed-time 15 --speed-limit 128 --max-filesize 1048576 -w '%{http_code}' -o "$2" "$1" 2>"$errfile") || rc=$?
                ;;
            busybox)
                "$BB" wget -q -T 20 -O "$2" "$1" 2>"$errfile" || rc=$?
                ;;
            wget)
                wget -q -T 20 -O "$2" "$1" 2>"$errfile" || rc=$?
                ;;
        esac
        if [ "$rc" -ne 0 ]; then
            say "  download failed: $downloader exit $rc${http:+, HTTP $http}"
            # Downloader diagnostics only, never the response body/private key.
            if [ -s "$errfile" ]; then
                head -c 1024 "$errfile" | head -n 3 | while IFS= read -r line; do say "  $line"; done
            fi
            continue
        fi
        [ -s "$2" ] || { say "  $downloader returned an empty response"; continue; }
        size=$(wc -c < "$2")
        [ "$size" -le 1048576 ] || { say "  response exceeds 1 MiB"; rm -f "$2"; return 1; }
        rm -f "$errfile"
        say "  downloaded $size bytes with $downloader"
        return 0
    done
    rm -f "$2" "$WORK/downloader-error"
    [ "$attempted" = 1 ] || say "  download failed: no supported downloader"
    return 1
)

dl() { # dl <url> <out>
    for u in $(candidates "$1"); do
        if dl_one "$u" "$2"; then
            [ "$u" != "$1" ] && say "  fetched via $u"
            return 0
        fi
    done
    return 1
}

# Every source candidate passes cleanup, backend rules, validity and revocation.
# sources.sh retries the next Spectre candidate when this callback fails.
kbl_validate_candidate() (
    candidate=$1
    cleaned="$candidate.prepared"
    if ! "$BIN" prepare "$BACKEND" < "$candidate" > "$cleaned"; then
        rm -f "$cleaned"
        say "Candidate is incompatible with $BACKEND; trying next."
        exit 1
    fi
    mv -f "$cleaned" "$candidate" || exit 1
    KB_ACCEL="$ACCEL" "$BIN" verify "$candidate"
    rev=$?
    case "$rev" in
        0) say "Certificate checks: OK."; exit 0 ;;
        1) say "Candidate is REVOKED/SUSPENDED; trying next." ;;
        3) say "Candidate is expired or not yet valid (device time); trying next." ;;
        *) say "Candidate verification failed (exit $rev); old keybox retained." ;;
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
    say "Installing independently for $BACKEND..."
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
    say "Done. Installed directly to $TARGET."
    return 0
}

# ---------------------------------------------------------------- main
OUT="$WORK/keybox.xml"
for src in $SOURCES; do
    say "-> trying source: $src"
    rm -f "$OUT" 2>/dev/null

    if ! kbl_fetch_source "$src" "$OUT"; then
        say "Source '$src' failed; trying next."
        continue
    fi
    say "Source '$src' passed backend and certificate checks ($(wc -c < "$OUT" | tr -d ' ') bytes)"

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
done

die "all sources failed (network, backend compatibility or certificate checks); old keybox retained"
