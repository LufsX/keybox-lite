#!/system/bin/sh
# Standalone WebUI entry, also linked from supported keystore modules on Magisk.
entry_dir="${0%/*}"
[ "$entry_dir" != "$0" ] || entry_dir="."
entry_dir="$(CDPATH= cd "$entry_dir" && pwd -P)" || exit $?
entry_id="${entry_dir##*/}"
case "$entry_id" in
    tricky_store|oh_my_keymint|teesim)
        exec "/data/adb/modules/keybox-lite/bin/keyboxlite" open-webui --module "$entry_id" "$@"
        ;;
    *) exec "/data/adb/modules/keybox-lite/bin/keyboxlite" open-webui "$@" ;;
esac
