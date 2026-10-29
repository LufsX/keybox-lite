#!/bin/sh
# Run centralized tests in their production packages using Go's build overlay.
set -eu
TEST_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
SOURCE_DIR=$(CDPATH= cd -- "$TEST_DIR/.." && pwd -P)
MODE=${1:-test}
if [ "$#" -gt 0 ]; then shift; fi
case "$MODE" in
    test|vet|list) ;;
    -h|--help)
        printf '%s\n' 'Usage: sh src/test/run.sh [test|vet|list] [Go flags and packages relative to src]'
        exit 0
        ;;
    *) printf '%s\n' "unsupported Go command: $MODE" >&2; exit 2 ;;
esac
for arg do
    case "$arg" in
        -overlay|-overlay=*) printf '%s\n' 'the test runner supplies -overlay' >&2; exit 2 ;;
    esac
done
ORIGINAL_UMASK=$(umask)
umask 077
OVERLAY_DIR=$(mktemp -d "${TMPDIR:-/tmp}/keybox-go-tests.XXXXXXXX")
trap 'rm -rf "$OVERLAY_DIR"' EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
(cd "$TEST_DIR" && go run ./overlay.go "$SOURCE_DIR" "$TEST_DIR") > "$OVERLAY_DIR/overlay.json"
umask "$ORIGINAL_UMASK"
cd "$SOURCE_DIR"
if [ "$#" -eq 0 ]; then set -- ./...; fi
go "$MODE" -overlay "$OVERLAY_DIR/overlay.json" "$@"
