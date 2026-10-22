#!/system/bin/sh
# Keybox Lite scheduler: runs action.sh when the configured interval has elapsed.
# Lightweight: no network, no wakelock, one local check every 30 minutes.
MODDIR=$(dirname "$0")
CONF=/data/adb/keybox-lite.conf
LOG=/data/local/tmp/keybox-update.log
LOCK=/data/local/tmp/keybox-scheduler.lock

interval_for() {
    case "$1" in
        daily) echo 86400 ;;
        weekly) echo 604800 ;;
        *) echo 0 ;;
    esac
}

SCHEDULE=off
LAST_RUN=0
[ -f "$CONF" ] && . "$CONF" 2>/dev/null
[ -n "$SCHEDULE" ] || SCHEDULE=off
[ -n "$LAST_RUN" ] || LAST_RUN=0

INTERVAL=$(interval_for "$SCHEDULE")
[ "$INTERVAL" -gt 0 ] || exit 0

mkdir "$LOCK" 2>/dev/null || exit 0

(
    while true; do
        now=$(date +%s)
        if [ $((now - LAST_RUN)) -ge "$INTERVAL" ]; then
            sh "$MODDIR/action.sh" >> "$LOG" 2>&1
            LAST_RUN=$(date +%s)
            printf 'SCHEDULE=%s\nLAST_RUN=%s\n' "$SCHEDULE" "$LAST_RUN" > "$CONF"
        fi
        sleep 1800
        SCHEDULE=off
        LAST_RUN=0
        [ -f "$CONF" ] && . "$CONF" 2>/dev/null
        [ -n "$SCHEDULE" ] || SCHEDULE=off
        INTERVAL=$(interval_for "$SCHEDULE")
        if [ "$INTERVAL" -le 0 ]; then
            rm -rf "$LOCK"
            exit 0
        fi
    done
) &
exit 0
