#!/bin/sh
# Build and assemble a module zip from an explicit runtime file list.
set -eu
cd "$(dirname "$0")"
sh build.sh
if [ ! -d webui/node_modules ]; then
    (cd webui && npm ci --no-audit --no-fund)
fi
(cd webui && npm run build)
cp -f webui/public/icon.png icon.png
VERSION=$(sed -n 's/^version=//p' module.prop)
case "$VERSION" in
    ""|*[!A-Za-z0-9._-]*) echo "invalid module version" >&2; exit 1 ;;
esac
VERSION_CODE=$(date '+%Y%m%d%H%M')
mkdir -p dist
OUT="dist/keybox-lite-$VERSION.zip"
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT
trap 'exit 1' HUP INT TERM
mkdir -p "$STAGE/bin"
cp module.prop action.sh settings.sh service.sh customize.sh uninstall.sh webui.sh icon.png "$STAGE/"
sed "s/^versionCode=.*/versionCode=$VERSION_CODE/" module.prop > "$STAGE/module.prop"
cp -R META-INF webroot "$STAGE/"
# The module manager uses the root PNG; the WebUI uses icon.svg.
rm -f "$STAGE/webroot/icon.png"
if [ -f bin/keyboxlite.upx ]; then
    cp bin/keyboxlite.upx "$STAGE/bin/keyboxlite"
else
    cp bin/keyboxlite "$STAGE/bin/keyboxlite"
fi
DEST="$PWD/$OUT"
(cd "$STAGE" && zip -9 -r -X -D module.zip module.prop action.sh settings.sh service.sh customize.sh uninstall.sh webui.sh icon.png bin META-INF webroot >/dev/null)
mv -f "$STAGE/module.zip" "$DEST"
echo "packaged $OUT ($(wc -c < "$OUT") bytes)"
