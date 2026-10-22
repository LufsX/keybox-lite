#!/system/bin/sh
# One scheduler per module. Failed updates retry at the next 30-minute check.
MODDIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd) || exit 1
KBL_LIBRARY=1
. "$MODDIR/settings.sh" || exit 1
unset KBL_LIBRARY
kbl_init && kbl_enabled && kbl_read_conf || exit 1
case "$SCHEDULE" in daily|weekly) ;; *) exit 0 ;; esac

if [ "$1" != --worker ]; then
    sh "$MODDIR/service.sh" --worker >/dev/null 2>&1 &
    exit 0
fi

[ ! -L "$STATE/scheduler.lock" ] || exit 1
exec 9>> "$STATE/scheduler.lock" || exit 1
kbl_flock -n 9 || exit 0
CHILD=
cleanup() {
    trap '' HUP INT TERM
    if [ -n "$CHILD" ]; then
        kill -TERM "$CHILD" 2>/dev/null
        wait "$CHILD" 2>/dev/null
    fi
    rm -f "$STATE/scheduler.pid" "$STATE/scheduler.pid.$$"
}
trap cleanup 0
trap 'exit 1' HUP INT TERM
start=$(kbl_process_token "$$") || exit 1
printf '%s %s\n' "$$" "$start" > "$STATE/scheduler.pid.$$" &&
    mv -f "$STATE/scheduler.pid.$$" "$STATE/scheduler.pid" || exit 1

while kbl_enabled; do
    kbl_read_conf || exit 1
    case "$SCHEDULE" in
        daily) INTERVAL=86400 ;;
        weekly) INTERVAL=604800 ;;
        *) exit 0 ;;
    esac
    now=$(date +%s) || exit 1
    kbl_timestamp "$now" || exit 1
    now=$kbl_number
    if [ "$LAST_RUN" -gt "$now" ] || [ $((now - LAST_RUN)) -ge "$INTERVAL" ]; then
        # action.sh writes its own log; redirecting stdout there duplicates each
        # line and races its log replacement. Children must not retain fd 9.
        sh "$MODDIR/action.sh" >/dev/null 2>&1 9>&- &
        CHILD=$!
        wait "$CHILD"
        result=$?
        CHILD=
        if [ "$result" -eq 0 ]; then
            completed=$(date +%s) || exit 1
            kbl_timestamp "$completed" || exit 1
            completed=$kbl_number
            kbl_config_lock || exit 1
            # Reload under the same lock as settings.set, preserving changes
            # made while the download was running, including SCHEDULE=off.
            if kbl_enabled && kbl_read_conf; then
                LAST_RUN=$completed
                kbl_write_conf || { kbl_config_unlock; exit 1; }
            fi
            kbl_config_unlock
        fi
    fi
    sleep 1800 9>&- &
    CHILD=$!
    wait "$CHILD"
    CHILD=
done
