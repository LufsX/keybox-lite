#!/bin/sh
# Build the Go binary, pack it with UPX, and assemble the module zip into dist/.
set -e
cd "$(dirname "$0")"
sh build.sh
cp -f bin/keyboxlite.upx bin/keyboxlite
cp -f webui/public/icon.png icon.png
VERSION=$(sed -n 's/^version=//p' module.prop)
mkdir -p dist
OUT="dist/keybox-lite-$VERSION.zip"
rm -f "$OUT"
zip -r -X "$OUT" . \
  -x 'src/*' 'webui/*' 'dist/*' 'build.sh' 'package.sh' 'Makefile' 'README.md' '.gitignore' '.git/*' 'bin/keyboxlite.upx' '*.DS_Store' >/dev/null
echo "packaged $OUT ($(wc -c < "$OUT") bytes)"
