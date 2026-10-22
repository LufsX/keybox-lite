#!/bin/sh
# Run with sh and with Android-like mksh: mksh tests/lock-compatibility.sh.
set -eu
REPO=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SANDBOX=$(mktemp -d)
trap 'rm -rf "$SANDBOX"' 0
trap 'exit 1' HUP INT TERM
# Load the exact production helper without running any Android operations.
KBL_LIBRARY=1
. "$REPO/settings.sh"
unset KBL_LIBRARY
STATE=$SANDBOX
KBL_FLOCK=$(command -v flock)
KBL_BB=
assert_held() {
    if "$KBL_FLOCK" -n "$1" true; then
        echo "FAIL: lock was not retained by parent shell" >&2
        exit 1
    fi
}
assert_free() { "$KBL_FLOCK" -n "$1" true; }

exec 7>> "$STATE/action.lock"
kbl_flock -n 7
assert_held "$STATE/action.lock"
# A second shell-opened description must contend, not accidentally share it.
exec 9>> "$STATE/action.lock"
if kbl_flock -n 9; then echo "FAIL: duplicate lock accepted"; exit 1; fi
exec 9>&-
kbl_flock -u 7
assert_free "$STATE/action.lock"
kbl_flock 7
assert_held "$STATE/action.lock"
exec 7>&-
assert_free "$STATE/action.lock"

kbl_config_lock
assert_held "$STATE/config.lock"
kbl_config_unlock
assert_free "$STATE/config.lock"

exec 9>> "$STATE/scheduler.lock"
kbl_flock -n 9
assert_held "$STATE/scheduler.lock"
exec 9>&-
assert_free "$STATE/scheduler.lock"

# Exercise the BusyBox dispatch branch using a strict applet adapter.
REAL_FLOCK=$KBL_FLOCK
export REAL_FLOCK
cat > "$SANDBOX/busybox" <<'SH'
#!/bin/sh
[ "$1" = flock ] || exit 2
shift
exec "$REAL_FLOCK" "$@"
SH
chmod +x "$SANDBOX/busybox"
KBL_BB="$SANDBOX/busybox"
KBL_FLOCK=
exec 7>> "$STATE/action.lock"
kbl_flock -n 7
if "$REAL_FLOCK" -n "$STATE/action.lock" true; then echo "FAIL: applet lock lost"; exit 1; fi
kbl_flock -u 7
"$REAL_FLOCK" -n "$STATE/action.lock" true
exec 7>&-
echo "PASS: action/config/scheduler locks, contention, unlock/close release, applet dispatch"
