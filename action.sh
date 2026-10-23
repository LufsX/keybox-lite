#!/system/bin/sh
MODDIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd) || exit 1
BIN="$MODDIR/bin/keyboxlite"
[ -x "$BIN" ] || BIN="$MODDIR/keyboxlite"
[ -x "$BIN" ] || BIN="$(command -v keyboxlite 2>/dev/null)"
[ -n "$BIN" ] && [ -x "$BIN" ] || { echo "ERROR: keyboxlite binary not found" >&2; exit 1; }
if [ "$1" = --managed ]; then
    exec "$BIN" update "$MODDIR"
fi
exec "$BIN" run-update "$MODDIR/action.sh" /data/adb/keybox-lite
