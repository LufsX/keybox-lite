#!/bin/sh
# Host-only Go regression tests; all writes and downloaders use a private fixture.
set -eu
REPO=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$REPO/src"
exec go test -count=1 ./internal/runtime -run '^TestSourceRace' -v
