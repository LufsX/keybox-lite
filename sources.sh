#!/system/bin/sh
# Source download/decoding library. Caller supplies BIN, WORK and dl <url> <out>.
# No installation or network work occurs when this file is sourced.
# kbl_fetch_source <meow|spectre|addon> <out> publishes only a complete decode.
# The caller must validate certificates/revocation before installation. Defining
# kbl_validate_candidate <file> additionally enables fallback after validation failure.

kbl_source_note() {
    if command -v say >/dev/null 2>&1; then say "$*"; else printf '%s\n' "$*" >&2; fi
}

kbl_fetch_source() (
    [ "$#" -eq 2 ] && [ -n "$WORK" ] && [ -d "$WORK" ] && [ -x "$BIN" ] || return 1
    kbl_source=$1
    kbl_output=$2
    [ ! -d "$kbl_output" ] && [ ! -L "$kbl_output" ] || return 1
    case "$kbl_source" in meow|spectre|addon) ;; *) return 1 ;; esac
    umask 077
    kbl_source_work=$(mktemp -d "$WORK/source.XXXXXX") || return 1
    trap 'rm -rf "$kbl_source_work"' 0
    trap 'exit 1' HUP INT TERM

    kbl_decode_candidate() {
        dl "$1" "$kbl_source_work/encoded" || return 1
        if "$BIN" "$2" < "$kbl_source_work/encoded" > "$kbl_source_work/decoded" 2>/dev/null; then :
        else rc=$?; kbl_source_note "  $kbl_source decode failed: $2 exit $rc"; return 1; fi
        [ -s "$kbl_source_work/decoded" ] || { kbl_source_note "  $kbl_source decode failed: empty output"; return 1; }
        if command -v kbl_validate_candidate >/dev/null 2>&1; then
            kbl_validate_candidate "$kbl_source_work/decoded" || return 1
        fi
        mv -f "$kbl_source_work/decoded" "$kbl_output"
    }

    case "$kbl_source" in
        meow)
            kbl_decode_candidate "${MEOW_URL:-https://github.com/MeowDump/Integrity-Box/releases/download/archive/Megatron}" decode-meow
            ;;
        addon)
            # Duck ToolBox state.rs default provider; no shell pipeline execution.
            kbl_decode_candidate "${ADDON_URL:-https://raw.githubusercontent.com/KOWX712/Tricky-Addon-Update-Target-List/keybox/.extra}" decode-addon
            ;;
        spectre)
            dl "${SPECTRE_CATALOG:-https://rawbin.dpejoh.com/catalog}" "$kbl_source_work/catalog.json" || return 1
            if "$BIN" spectre-urls "$kbl_source_work/catalog.json" "${SPECTRE_BASE:-https://rawbin.dpejoh.com/key}" > "$kbl_source_work/urls" 2>/dev/null; then :
            else rc=$?; kbl_source_note "  spectre catalog rejected or no eligible candidates (exit $rc)"; return 1; fi
            while IFS= read -r kbl_url; do
                [ -n "$kbl_url" ] || continue
                if kbl_decode_candidate "$kbl_url" decode-spectre; then
                    return 0
                fi
            done < "$kbl_source_work/urls"
            return 1
            ;;
    esac
)
