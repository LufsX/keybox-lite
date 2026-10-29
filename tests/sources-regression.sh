#!/bin/sh
# Host-only Go regression tests; all writes and downloaders use a private fixture.
set -eu
REPO=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
exec sh "$REPO/src/test/run.sh" test -count=1 ./internal/runtime -run '^TestSources' -v
