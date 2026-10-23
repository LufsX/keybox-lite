#!/system/bin/sh
# Keybox Lite - installer

ui_print " "
ui_print "- Keybox Lite"

if [ "$KSU" = "true" ]; then
    ui_print "- Root manager: KernelSU $KSU_VER"
elif [ "$APATCH" = "true" ]; then
    ui_print "- Root manager: APatch $APATCH_VER"
else
    ui_print "- Root manager: Magisk $MAGISK_VER"
fi

for kbl_file in action.sh settings.sh service.sh uninstall.sh webui.sh bin/keyboxlite; do
    [ -f "$MODPATH/$kbl_file" ] || abort "! Missing $kbl_file"
done
for kbl_file in action.sh settings.sh service.sh uninstall.sh webui.sh bin/keyboxlite; do
    set_perm "$MODPATH/$kbl_file" 0 0 0755 || abort "! Cannot set permissions: $kbl_file"
done
# uninstall leaves this marker to block late writers; reinstall enables runtime.
rm -f /data/adb/keybox-lite/stopping || abort "! Cannot enable Keybox Lite"

ui_print "- Standalone backend detection, validation and backed-up installation"
ui_print "- Supports Tricky Store, OhMyKeymint and TEESimulator"

ui_print "- Use the module Action button to install a keybox."
ui_print " "
