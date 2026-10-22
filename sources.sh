#!/system/bin/sh
# Source download/decoding library. Caller supplies BIN, WORK and dl <url> <out>.
#
# The backend compatibility rules, keybox cleanup behavior, and built-in source
# formats were adapted from / implemented with reference to Duck-ToolBox
# (https://github.com/eltavine/Duck-ToolBox).
# Reference revision: 7567a8e084d31b1a6da3541bf0e8e2692ee45e0a.
#
# MIT License
#
# Copyright (c) 2026 Eltavine (尉迟栩澜)
#
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in all
# copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
# SOFTWARE.
#
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
    # Decoders and verify subprocesses must not share temporary state with
    # another source/route, even when the caller runs candidates concurrently.
    TMPDIR=$kbl_source_work
    export TMPDIR

    kbl_decode_candidate() {
        if command -v kbl_source_rejected >/dev/null 2>&1; then
            kbl_source_rejected && return 1
        fi
        dl "$1" "$kbl_source_work/encoded" || return 1
        if "$BIN" "$2" < "$kbl_source_work/encoded" > "$kbl_source_work/decoded" 2>/dev/null; then :
        else rc=$?; kbl_source_note "  $kbl_source decode failed: $2 exit $rc"; return 1; fi
        [ -s "$kbl_source_work/decoded" ] || { kbl_source_note "  $kbl_source decode failed: empty output"; return 1; }
        if command -v kbl_validate_candidate >/dev/null 2>&1; then
            kbl_validate_candidate "$kbl_source_work/decoded" || return 1
        fi
        if command -v kbl_source_rejected >/dev/null 2>&1; then
            kbl_source_rejected && return 1
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
                if command -v kbl_source_rejected >/dev/null 2>&1; then
                    kbl_source_rejected && return 1
                fi
                [ -n "$kbl_url" ] || continue
                if kbl_decode_candidate "$kbl_url" decode-spectre; then
                    return 0
                fi
            done < "$kbl_source_work/urls"
            return 1
            ;;
    esac
)
