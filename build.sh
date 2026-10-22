#!/bin/sh
# Build the keyboxlite binary (static arm64 for Android) and shrink it with UPX.
set -e
cd "$(dirname "$0")"
mkdir -p bin
(cd src && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o ../bin/keyboxlite .)
echo "built bin/keyboxlite ($(wc -c < bin/keyboxlite) bytes)"
if command -v upx >/dev/null 2>&1; then
    rm -f bin/keyboxlite.upx
    upx --best --lzma -q -o bin/keyboxlite.upx bin/keyboxlite
    echo "packed bin/keyboxlite.upx ($(wc -c < bin/keyboxlite.upx) bytes)"
fi
