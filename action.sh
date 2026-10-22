#!/system/bin/sh
# Keybox Lite - Action button: fetch, decode, validate and install a keybox.
#
# Downloads use curl/wget (DNS+TLS solved); decoding, ASN.1 serial extraction and the
# Google revocation check (status download included) run in the bundled keyboxlite binary.
#
# Only knobs; edit here.
SOURCES="meow spectre"
ACCEL="https://c.etc.moe/"

[ -n "$TMPDIR" ] || TMPDIR=/data/local/tmp

MODDIR=$(dirname "$0")
BIN="$MODDIR/bin/keyboxlite"
[ -x "$BIN" ] || BIN="$MODDIR/keyboxlite"
[ -x "$BIN" ] || BIN="$(command -v keyboxlite 2>/dev/null)"
[ -n "$BIN" ] && [ -x "$BIN" ] || { echo "ERROR: keyboxlite binary not found next to action.sh"; exit 1; }

WORK="$TMPDIR/keybox-lite.$$"
LOG="$TMPDIR/keybox-update.log"

DUCKCTL="/data/adb/modules/duck-toolbox/bin/duckctl.sh"
DUCKCTL_ALT="/data/adb/modules_update/duck-toolbox/bin/duckctl.sh"

MEOW_URL="https://github.com/MeowDump/Integrity-Box/releases/download/archive/Megatron"
SPECTRE_BASE="https://rawbin.dpejoh.com/key"
SPECTRE_CATALOG="https://rawbin.dpejoh.com/catalog"

# Action shells may run with a minimal PATH, so locate busybox for wget.
BB=""
for c in /data/adb/ksu/bin/busybox /data/adb/ap/bin/busybox /data/adb/magisk/busybox /system/bin/busybox /system/xbin/busybox; do
    [ -x "$c" ] && { BB="$c"; break; }
done
[ -n "$BB" ] || BB="$(command -v busybox 2>/dev/null)"

say() { echo "$*"; echo "$(date '+%F %T') | $*" >> "$LOG" 2>/dev/null; }
die() { say "ERROR: $*"; cleanup; exit 1; }
cleanup() { rm -rf "$WORK" 2>/dev/null; }

mkdir -p "$WORK" 2>/dev/null || { echo "cannot create workdir $WORK"; exit 1; }
: > "$LOG" 2>/dev/null

say "==========================================="
say " Keybox Lite  |  sources: $SOURCES"
say " accel:        $ACCEL"
say " busybox:      $BB"
say "==========================================="

# ---------------------------------------------------------------- download (accel then direct)
candidates() { # candidates <url>
    u="$1"
    if [ -n "$ACCEL" ]; then
        for p in $ACCEL; do printf '%s%s\n' "$p" "$u"; done
    fi
    printf '%s\n' "$u"
}

dl_one() { # dl_one <url> <out>
    rm -f "$2" 2>/dev/null
    if command -v curl >/dev/null 2>&1; then
        curl -fSL --connect-timeout 15 --max-time 180 -o "$2" "$1" 2>/dev/null
    elif [ -n "$BB" ] && "$BB" wget --help >/dev/null 2>&1; then
        "$BB" wget -q -T 60 -t 1 -O "$2" "$1" 2>/dev/null
    elif command -v wget >/dev/null 2>&1; then
        wget -q --timeout=60 --tries=1 -O "$2" "$1" 2>/dev/null
    else
        return 1
    fi
    [ -s "$2" ]
}

dl() { # dl <url> <out>
    for u in $(candidates "$1"); do
        if dl_one "$u" "$2"; then
            [ "$u" != "$1" ] && say "  fetched via $u"
            return 0
        fi
    done
    return 1
}

# ---------------------------------------------------------------- fetch + decode (binary)
try_fetch() { # try_fetch <source> <out>
    src="$1"; out="$2"
    case "$src" in
        meow)
            enc="$WORK/meow.enc"
            dl "$MEOW_URL" "$enc" || return 1
            "$BIN" decode-meow < "$enc" > "$out" 2>/dev/null
            ;;
        spectre)
            catf="$WORK/catalog.json"
            dl "$SPECTRE_CATALOG" "$catf" || return 1
            purl=$("$BIN" spectre-url "$catf" "$SPECTRE_BASE") || return 1
            enc="$WORK/spectre.enc"
            dl "$purl" "$enc" || return 1
            "$BIN" decode-spectre < "$enc" > "$out" 2>/dev/null
            ;;
        *)
            return 1
            ;;
    esac
}

valid_keybox() { # cheap structural check before the revocation step
    [ -s "$1" ] || return 1
    grep -q '<AndroidAttestation' "$1" || return 1
    grep -q '</AndroidAttestation>' "$1" || return 1
    return 0
}

# returns 0 = not revoked, 1 = revoked/suspended, 2 = could not check
check_revoked() { # check_revoked <keybox>
    KB_ACCEL="$ACCEL" "$BIN" verify "$1"
    return $?
}

# ---------------------------------------------------------------- install
DUCK=""
if [ -x "$DUCKCTL" ]; then DUCK="$DUCKCTL"
elif [ -x "$DUCKCTL_ALT" ]; then DUCK="$DUCKCTL_ALT"
fi

detect_target() {
    if [ -d /data/adb/modules/tricky_store ]; then echo "/data/adb/tricky_store/keybox.xml"; return; fi
    if [ -d /data/adb/modules/teesim ]; then echo "/data/adb/teesim/keybox.xml"; return; fi
    if [ -d /data/adb/modules/oh_my_keymint ]; then echo "/data/misc/keystore/omk/keybox.xml"; return; fi
    echo ""
}

install_keybox() { # install_keybox <file>
    if [ -n "$DUCK" ]; then
        say "Installing through Duck ToolBox..."
        if "$DUCK" tricky-store keybox install "$1"; then
            say "Done. Installed via Duck ToolBox."
            return 0
        fi
        say "duckctl install failed; falling back to direct write."
    fi
    TARGET="$(detect_target)"
    [ -n "$TARGET" ] || die "no keystore backend found (Tricky Store / TEESimulator / OhMyKeymint)"
    mkdir -p "$(dirname "$TARGET")" 2>/dev/null
    if [ -s "$TARGET" ]; then
        BAK="$TARGET.$(date +%Y%m%d_%H%M%S).bak"
        cp -f "$TARGET" "$BAK" && say "Backed up old keybox -> $BAK"
    fi
    cp -f "$1" "$TARGET" || die "cannot write $TARGET"
    chmod 644 "$TARGET" 2>/dev/null
    say "Done. Installed directly to $TARGET."
    return 0
}

# ---------------------------------------------------------------- main
OUT="$WORK/keybox.xml"
for src in $SOURCES; do
    say "-> trying source: $src"
    rm -f "$OUT" 2>/dev/null

    if ! try_fetch "$src" "$OUT" || ! valid_keybox "$OUT"; then
        say "Source '$src' failed; trying next."
        continue
    fi
    say "Source '$src' produced a valid keybox ($(wc -c < "$OUT" | tr -d ' ') bytes)"

    check_revoked "$OUT"
    rev=$?
    if [ "$rev" -eq 1 ]; then
        say "Source '$src' keybox is REVOKED/SUSPENDED; trying next."
        continue
    elif [ "$rev" -eq 2 ]; then
        say "Revocation check unavailable; installing without it."
    else
        say "Revocation check: OK."
    fi

    install_keybox "$OUT"
    printf '%s | %s | %s bytes | installed\n' "$(date '+%F %T')" "$src" "$(wc -c < "$OUT" | tr -d ' ')" >> "$TMPDIR/keybox-update.history" 2>/dev/null
    cleanup
    exit 0
done

die "all sources failed (check network)"
