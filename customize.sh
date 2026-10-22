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

set_perm "$MODPATH/action.sh" 0 0 0755
set_perm "$MODPATH/settings.sh" 0 0 0755
set_perm "$MODPATH/service.sh" 0 0 0755
set_perm "$MODPATH/uninstall.sh" 0 0 0755
set_perm "$MODPATH/bin/keyboxlite" 0 0 0755

if [ -x /data/adb/modules/duck-toolbox/bin/duckctl.sh ]; then
    ui_print "- Duck ToolBox found; installs go through duckctl (validated + backed up)"
fi

ui_print "- Use the module Action button to install a keybox."
ui_print " "
