#!/system/bin/sh
# Keybox Lite settings (read/write the schedule).
MODDIR=$(dirname "$0")
CONF=/data/adb/keybox-lite.conf

SCHEDULE=off
LAST_RUN=0
read_conf() {
    SCHEDULE=off
    LAST_RUN=0
    [ -f "$CONF" ] && . "$CONF" 2>/dev/null
    [ -n "$SCHEDULE" ] || SCHEDULE=off
    [ -n "$LAST_RUN" ] || LAST_RUN=0
}

case "$1" in
    get)
        read_conf
        echo "SCHEDULE=$SCHEDULE"
        echo "LAST_RUN=$LAST_RUN"
        ;;
    set)
        case "$2" in
            off|daily|weekly) ;;
            *) echo "invalid schedule"; exit 2 ;;
        esac
        read_conf
        printf 'SCHEDULE=%s\nLAST_RUN=%s\n' "$2" "$LAST_RUN" > "$CONF"
        echo "SCHEDULE=$2"
        # start the scheduler if it is not already running
        sh "$MODDIR/service.sh" >/dev/null 2>&1 &
        ;;
    *)
        echo "usage: settings.sh get | set <off|daily|weekly>"
        ;;
esac
