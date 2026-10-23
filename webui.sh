#!/system/bin/sh
# Standalone WebUI entry, also linked from supported keystore modules on Magisk.
exec /data/adb/modules/keybox-lite/bin/keyboxlite open-webui "$@"
