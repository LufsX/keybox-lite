# Keybox Lite

A lightweight KernelSU / APatch / Magisk module that updates the Tricky Store / TEESimulator
/ OhMyKeymint keybox from the module Action button or the built-in WebUI.

- **Sources**: MeowDump "Megatron" and dpejoh "spectre", tried in order.
- **Decoding / ASN.1 / revocation check** run in a small Go binary (`bin/keyboxlite`, static arm64).
  Semantics: base64 x10 + hex + ROT13, shuffled base64, X.509 serial extraction, Google
  attestation status matching (live, then a stale GitHub mirror).
- **Downloads** stay in the shell: curl/wget already solve Android's DNS and TLS, and a Go
  `net/http` client would cost ~1 MB more and still need a resolver.
- **Install** prefers Duck ToolBox's `duckctl` (backend-aware, validated, backed up) and
  falls back to a direct write.

## Layout

    action.sh      Action button: fetch -> decode -> verify -> install
    settings.sh    read/write the auto-update schedule
    service.sh     scheduler: runs action.sh when the interval elapses
    customize.sh   installer
    uninstall.sh
    module.prop
    bin/keyboxlite Go binary (built, not tracked)
    src/           Go source (internal/decode, keybox, catalog, status, verify, info)
    webui/         Svelte 5 + Tailwind v4 + TS + lucide-svelte WebUI
    webroot/       WebUI build output (bundled into the zip)
    META-INF/      module installer

## Build

    sh build.sh                 # Go binary + UPX
    cd webui && npm run build   # WebUI -> webroot/
    sh package.sh               # dist/keybox-lite-<version>.zip

## WebUI

Shows the installed keybox (path, size, serials, structural validity), one-tap update and
revocation check, an auto-update schedule (off / daily / weekly) and the module's update
records. Calls the shell through the official `kernelsu` bridge using `spawn` (not `exec`)
so a long command never freezes the WebView. Supports light/dark and English/Chinese.
