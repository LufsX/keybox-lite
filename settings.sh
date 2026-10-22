#!/system/bin/sh
# Schedule settings and shared runtime helpers (also sourced by the other scripts).
MODDIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd) || exit 1
CONF=/data/adb/keybox-lite.conf
STATE=/data/adb/keybox-lite

kbl_init() {
    umask 077
    [ ! -L "$STATE" ] && mkdir -p "$STATE" && chmod 700 "$STATE" || return 1
    KBL_FLOCK=$(command -v flock 2>/dev/null)
    [ -z "$KBL_FLOCK" ] || return 0
    for KBL_BB in /data/adb/ksu/bin/busybox /data/adb/ap/bin/busybox /data/adb/magisk/busybox /system/bin/busybox /system/xbin/busybox "$(command -v busybox 2>/dev/null)"; do
        if [ -x "$KBL_BB" ] && "$KBL_BB" flock --help >/dev/null 2>&1; then
            return 0
        fi
    done
    echo "ERROR: flock (or BusyBox flock) is required" >&2
    return 1
}

kbl_flock_command() {
    if [ -n "$KBL_FLOCK" ]; then "$KBL_FLOCK" "$@"
    else "$KBL_BB" flock "$@"
    fi
}

kbl_flock() {
    # Android mksh marks shell-opened high descriptors close-on-exec.
    # Duplicate the same open file description onto stdin for the external
    # utility; the shell retains its original fd and therefore the kernel lock.
    case "$#" in
        1) kbl_lock_fd=$1; set -- ;;
        2)
            case "$1" in -n|-u) ;; *) return 2 ;; esac
            kbl_lock_fd=$2
            set -- "$1"
            ;;
        *) return 2 ;;
    esac
    case "$kbl_lock_fd" in
        7) kbl_flock_command "$@" 0 <&7 ;;
        8) kbl_flock_command "$@" 0 <&8 ;;
        9) kbl_flock_command "$@" 0 <&9 ;;
        *) return 2 ;;
    esac
}

# Lock files stay in place: deleting a locked inode would allow a second owner.
# Kernel locks are automatically released on exit, SIGKILL and reboot.
kbl_config_lock() {
    [ ! -L "$STATE/config.lock" ] || return 1
    exec 8>> "$STATE/config.lock" || return 1
    kbl_tries=0
    until kbl_flock -n 8; do
        kbl_tries=$((kbl_tries + 1))
        if [ "$kbl_tries" -ge 10 ]; then exec 8>&-; return 1; fi
        sleep 1
    done
}

kbl_config_unlock() {
    kbl_flock -u 8
    exec 8>&-
}

kbl_timestamp() {
    case "$1" in ''|*[!0-9]*) return 1 ;; esac
    kbl_number=$1
    while [ "${kbl_number#0}" != "$kbl_number" ]; do kbl_number=${kbl_number#0}; done
    [ -n "$kbl_number" ] || kbl_number=0
    [ "${#kbl_number}" -le 10 ] || return 1
}

kbl_read_conf() {
    SCHEDULE=off
    LAST_RUN=0
    [ ! -L "$CONF" ] || return 1
    [ -e "$CONF" ] || return 0
    [ -f "$CONF" ] && [ -r "$CONF" ] || return 1
    # Parse data only. Never execute a root-owned configuration as shell code.
    while IFS= read -r kbl_line || [ -n "$kbl_line" ]; do
        case "$kbl_line" in
            SCHEDULE=off|SCHEDULE=daily|SCHEDULE=weekly) SCHEDULE=${kbl_line#*=} ;;
            LAST_RUN=*)
                if kbl_timestamp "${kbl_line#*=}"; then LAST_RUN=$kbl_number; fi
                ;;
        esac
    done < "$CONF"
}

# Caller holds fd 8. A same-directory rename exposes either complete version.
kbl_write_conf() (
    [ ! -d "$CONF" ] && [ ! -L "$CONF" ] || exit 1
    kbl_tmp=$(mktemp "$CONF.XXXXXX") || exit 1
    trap 'rm -f "$kbl_tmp"' 0
    trap 'exit 1' HUP INT TERM
    printf 'SCHEDULE=%s\nLAST_RUN=%s\n' "$SCHEDULE" "$LAST_RUN" > "$kbl_tmp" &&
        chmod 600 "$kbl_tmp" && mv -f "$kbl_tmp" "$CONF"
)

kbl_enabled() {
    [ ! -e "$STATE/stopping" ] && [ -d "$MODDIR" ] &&
        [ ! -e "$MODDIR/disable" ] && [ ! -e "$MODDIR/remove" ]
}

# PID plus /proc start time prevents signalling an unrelated reused PID.
kbl_process_token() {
    case "$1" in ''|*[!0-9]*) return 1 ;; esac
    [ "$1" -gt 1 ] || return 1
    IFS= read -r kbl_stat < "/proc/$1/stat" 2>/dev/null || return 1
    kbl_stat=${kbl_stat##*) }
    set -- $kbl_stat
    [ "$#" -ge 20 ] || return 1
    shift 19
    printf '%s\n' "$1"
}

kbl_stop_scheduler() {
    [ -f "$STATE/scheduler.pid" ] || return 0
    read -r kbl_pid kbl_start < "$STATE/scheduler.pid" || return 1
    kbl_current=$(kbl_process_token "$kbl_pid") || return 0
    [ "$kbl_current" != "$kbl_start" ] || kill -TERM "$kbl_pid" 2>/dev/null
}

[ "$KBL_LIBRARY" = 1 ] && return 0

kbl_init || exit 1
case "$1" in
    get)
        kbl_read_conf || { echo "ERROR: cannot read $CONF" >&2; exit 1; }
        printf 'SCHEDULE=%s\nLAST_RUN=%s\n' "$SCHEDULE" "$LAST_RUN"
        ;;
    set)
        case "$2" in
            off|daily|weekly) ;;
            *) echo "invalid schedule" >&2; exit 2 ;;
        esac
        kbl_enabled || { echo "ERROR: module is disabled or being removed" >&2; exit 1; }
        kbl_config_lock || { echo "ERROR: settings are busy" >&2; exit 1; }
        if ! kbl_enabled || ! kbl_read_conf; then kbl_config_unlock; exit 1; fi
        SCHEDULE=$2
        if ! kbl_write_conf; then
            kbl_config_unlock
            echo "ERROR: cannot save $CONF" >&2
            exit 1
        fi
        kbl_config_unlock
        echo "SCHEDULE=$SCHEDULE"
        if [ "$SCHEDULE" = off ]; then
            kbl_stop_scheduler
        else
            sh "$MODDIR/service.sh" >/dev/null 2>&1 8>&- &
        fi
        ;;
    *)
        echo "usage: settings.sh get | set <off|daily|weekly>" >&2
        exit 2
        ;;
esac
