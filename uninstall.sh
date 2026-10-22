#!/system/bin/sh
# Stop runtime writers before removing settings. Installed keyboxes are retained.
MODDIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd) || exit 1
KBL_LIBRARY=1
. "$MODDIR/settings.sh" || exit 1
unset KBL_LIBRARY
kbl_init || exit 1
: > "$STATE/stopping" || exit 1
kbl_stop_scheduler || exit 1

# Wait for any in-flight update to observe stopping and release its lock.
# Keep lock inodes and stopping in place until a subsequent installation.
exec 7>> "$STATE/action.lock" || exit 1
kbl_flock 7 || exit 1
exec 9>> "$STATE/scheduler.lock" || exit 1
kbl_flock 9 || exit 1
kbl_config_lock || exit 1
rm -f /data/local/tmp/keybox-update.log /data/local/tmp/keybox-update.history "$CONF"
result=$?
kbl_config_unlock
exit "$result"
