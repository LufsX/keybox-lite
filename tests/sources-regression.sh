#!/bin/sh
# Host-only source helper tests. Every download and CLI command is mocked;
# fixtures contain synthetic labels, never actual keys or certificates.
set -eu
REPO=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SANDBOX=$(mktemp -d)
trap 'rm -rf "$SANDBOX"' 0
trap 'exit 1' HUP INT TERM
fail() { echo "FAIL: $*" >&2; exit 1; }
ok() { echo "ok: $*"; }
BIN="$SANDBOX/keyboxlite"
WORK="$SANDBOX/work"
REQUESTS="$SANDBOX/requests"
VALIDATIONS="$SANDBOX/validations"
export REQUESTS VALIDATIONS
mkdir -p "$WORK"
cat > "$BIN" <<'MOCK'
#!/bin/sh
case "$1" in
    decode-meow|decode-spectre|decode-addon)
        data=$(cat)
        case "$data" in
            FAIL_DECODE) printf 'partial output'; exit 1 ;;
            EMPTY_DECODE) exit 0 ;;
            *) printf '%s\n' "$data" ;;
        esac
        ;;
    spectre-urls)
        [ "$(head -n 1 "$2")" != BAD_CATALOG ] || exit 1
        cat "$2"
        ;;
    *) exit 1 ;;
esac
MOCK
chmod +x "$BIN"

dl() {
    printf '%s\n' "$1" >> "$REQUESTS"
    case "$1" in
        */catalog)
            printf '%s\n' "$CATALOG_FIXTURE" > "$2"
            ;;
        */download-fails) printf 'partial download' > "$2"; return 1 ;;
        */decode-fails) printf '%s\n' FAIL_DECODE > "$2" ;;
        */decode-empty) printf '%s\n' EMPTY_DECODE > "$2" ;;
        */reject) printf '%s\n' REJECT > "$2" ;;
        */good) printf '%s\n' SYNTHETIC_ACCEPTED > "$2" ;;
        *) printf '%s\n' SYNTHETIC_DEFAULT > "$2" ;;
    esac
}
kbl_validate_candidate() {
    printf '%s\n' "$(cat "$1")" >> "$VALIDATIONS"
    [ "$(cat "$1")" != REJECT ] && [ "${VALIDATE_RC:-0}" = 0 ]
}
. "$REPO/sources.sh"

reset_case() {
    : > "$REQUESTS"
    : > "$VALIDATIONS"
    printf '%s\n' OLD_OUTPUT > "$SANDBOX/output"
    VALIDATE_RC=0
    unset MEOW_URL ADDON_URL SPECTRE_BASE SPECTRE_CATALOG
}
assert_clean() {
    [ -z "$(ls -A "$WORK")" ] || fail "temporary source files leaked"
}
assert_old() {
    [ "$(cat "$SANDBOX/output")" = OLD_OUTPUT ] || fail "failed source replaced output"
    assert_clean
}

reset_case
src=caller_source
out=caller_output
kbl_fetch_source addon "$SANDBOX/output" || fail "addon default failed"
[ "$(cat "$SANDBOX/output")" = SYNTHETIC_DEFAULT ] || fail "addon output"
[ "$(cat "$REQUESTS")" = https://raw.githubusercontent.com/KOWX712/Tricky-Addon-Update-Target-List/keybox/.extra ] || fail "addon URL differs from Duck provider"
[ "$src" = caller_source ] && [ "$out" = caller_output ] || fail "caller variables clobbered"
[ "$(cat "$VALIDATIONS")" = SYNTHETIC_DEFAULT ] || fail "addon skipped validation"
assert_clean
ok "Duck Addon provider URL, validation and variable isolation"

reset_case
CATALOG_FIXTURE='https://example.test/download-fails
https://example.test/decode-fails
https://example.test/decode-empty
https://example.test/reject
https://example.test/good
https://example.test/unused'
kbl_fetch_source spectre "$SANDBOX/output" || fail "spectre fallback failed"
[ "$(cat "$SANDBOX/output")" = SYNTHETIC_ACCEPTED ] || fail "spectre accepted bad candidate"
[ "$(wc -l < "$REQUESTS" | tr -d ' ')" = 6 ] || fail "spectre request order/count"
[ "$(cat "$VALIDATIONS")" = 'REJECT
SYNTHETIC_ACCEPTED' ] || fail "spectre validation order"
if grep -q unused "$REQUESTS"; then fail "spectre downloaded after acceptance"; fi
assert_clean
ok "spectre candidate fallback after download, decode and validation failures"

for source in meow addon; do
    for url in download-fails decode-fails decode-empty reject; do
        reset_case
        MEOW_URL="https://example.test/$url"
        ADDON_URL=$MEOW_URL
        if kbl_fetch_source "$source" "$SANDBOX/output"; then fail "$source accepted $url"; fi
        assert_old
    done
done
ok "failed single-source downloads/decodes/validation preserve prior output"

reset_case
CATALOG_FIXTURE='https://example.test/reject
https://example.test/reject'
if kbl_fetch_source spectre "$SANDBOX/output"; then fail "all rejected candidates accepted"; fi
assert_old
reset_case
CATALOG_FIXTURE=BAD_CATALOG
if kbl_fetch_source spectre "$SANDBOX/output"; then fail "bad catalog accepted"; fi
assert_old
ok "exhausted and invalid catalogs fail closed"

reset_case
VALIDATE_RC=3
if kbl_fetch_source meow "$SANDBOX/output"; then fail "time-invalid candidate accepted"; fi
assert_old
if kbl_fetch_source unknown "$SANDBOX/output"; then fail "unknown source accepted"; fi
mkdir "$SANDBOX/directory-output"
if kbl_fetch_source addon "$SANDBOX/directory-output"; then fail "directory output accepted"; fi
ln -s "$SANDBOX/output" "$SANDBOX/symlink-output"
if kbl_fetch_source addon "$SANDBOX/symlink-output"; then fail "symlink output accepted"; fi
assert_clean
ok "validator nonzero exits and unsafe output paths fail closed"

echo "Source regression tests passed."
