# Keybox Lite

A lightweight KernelSU / APatch / Magisk module that updates the Tricky Store / TEESimulator
/ OhMyKeymint keybox from the module Action button or the built-in WebUI.

- **Sources**: MeowDump "Megatron", then dpejoh "spectre", tried in order.
  The Tricky Addon decoder remains opt-in; its default endpoint currently serves
  an empty body and is excluded from automatic updates. Spectre tries each eligible candidate through
  cleanup, backend validation and certificate checks before accepting it.
- **Decoding / ASN.1 / revocation check** run in a small Go binary (`bin/keyboxlite`, static arm64).
  Semantics: base64 x10 + hex + ROT13, shuffled base64, X.509 serial extraction, Google
  attestation status matching (live, then a stale GitHub mirror).
- **Downloads** stay in the shell: curl/wget already solve Android's DNS and TLS, and a Go
  `net/http` client would cost ~1 MB more and still need a resolver.
- **Install** runs entirely within Keybox Lite. Duck ToolBox is not required and
  is never invoked, even if installed. Backend rules and paths follow its supported
  implementations. Every install backs up the old file and atomically replaces it,
  preserving its owner and mode; a new file uses mode 0644.
  An incompatible keybox or unavailable revocation check stops that candidate.
- **Runtime locking** requires `flock` or a root manager's BusyBox with its `flock`
  applet. Missing locking support produces an error before an update starts.

## Layout

    action.sh      Action button: detect -> fetch -> prepare -> verify -> install
    sources.sh     isolated source decoding and candidate fallback
    settings.sh    read/write the auto-update schedule
    service.sh     scheduler: runs action.sh when the interval elapses
    customize.sh   installer
    uninstall.sh
    module.prop
    bin/keyboxlite Go binary (built, not tracked)
    src/           Go source (internal/backend, decode, keybox, catalog, status, verify, info)
    webui/         Svelte 5 + Tailwind v4 + TS + lucide-svelte WebUI
    webroot/       WebUI build output (bundled into the zip)
    META-INF/      module installer

## Standalone backend compatibility

| Backend | Detection | Keybox destination | Additional keybox rules |
| --- | --- | --- | --- |
| Tricky Store | `tricky_store`, versionCode >= 246 | `/data/adb/tricky_store/keybox.xml` | At least one certified key |
| Tricky Store legacy | `tricky_store`, lower/missing versionCode | Same as above | At least one certified key |
| OhMyKeymint | `oh_my_keymint` | `/data/misc/keystore/omk/keybox.xml` | EC key required; each key needs a chain and correct declared certificate count |
| TEESimulator | `teesim` | `/data/adb/teesim/` + `profiles.default.keybox` from `config.json` (default `keybox.xml`) | Every key needs at least two certificates |

Detection uses installed modules with `module.prop`, preferring active Tricky Store,
then OhMyKeymint, then TEESimulator. Disabled/removal-pending modules are shown for
diagnostics but never installed into. Staged-only modules are not active. An unsafe
or malformed configuration is an error, not a reason to guess a different target.
Only TEESimulator’s default profile is updated; per-app custom profiles are not rewritten.

Before installation the XML is cleaned of comments/BOM/zero-width markers, structurally
validated, and checked against the selected backend. Keybox Lite checks PEM/DER shape
and key/leaf-certificate algorithm compatibility; it does not prove private/public
key correspondence, private-key arithmetic, chain signatures or trust.
The backend and target are rechecked before replacement. No daemon is restarted
automatically, matching Duck’s keybox installation behavior. Real Android reload,
SELinux labels and consumer access still need device testing.

Diagnostic commands:

    bin/keyboxlite backend                     # selected and installed backends as JSON
    bin/keyboxlite backend id                  # active backend ID
    bin/keyboxlite backend target              # safe active destination
    bin/keyboxlite prepare tricky-store < candidate.xml > prepared.xml

`backend` and `info` accept `--root <directory>` for host-only fixtures.
The production action never accepts a host sysroot.

## Stopping an update

Action-button, WebUI and scheduled updates run in a dedicated process group.
The WebUI “Stop update” button terminates that group, including download and verification
children; it can also stop a managed update started outside the WebUI. PID start-time
checks protect against stale records. It does not stop standalone certificate checks.
An update prints a heartbeat every 10 seconds and has a 20-minute overall deadline.
Curl uses an 8-second connection timeout, a 60-second total timeout and a 15-second
low-throughput timeout before fallback. Cancellation cannot undo an installation that
already completed. Updates started by older versions have no managed job record.

## Sources

| Source | Payload / metadata | Decoder |
| --- | --- | --- |
| Meow | `https://github.com/MeowDump/Integrity-Box/releases/download/archive/Megatron` | base64 ×10 → hex → ROT13 |
| Spectre | `https://rawbin.dpejoh.com/catalog`, payloads under `https://rawbin.dpejoh.com/key/` | shuffled base64 |
| Tricky Addon (disabled by default) | `https://raw.githubusercontent.com/KOWX712/Tricky-Addon-Update-Target-List/keybox/.extra` | hex → base64 |

Source downloads try direct access first, then configured proxies. For each URL,
failed curl attempts fall back to BusyBox wget and then standalone wget; logs show
which downloader ran and its error. Partial or empty failed responses are rejected.

Spectre prefers clean entries, then softbanned entries, excluding revoked entries and
ignoring unverified `working`/`autoOverride` hints. Download, decode or validation
failure advances to the next candidate. Catalog flags never replace certificate checks.
The downloader/decoder/parser limit source files to 1 MiB (wget size is checked after
download); all candidates still require current time validity and a usable revocation list.

The Addon URL and format match Duck’s default provider. An actual GET on 2026-10-22
returned HTTP 200 with a zero-byte body, both directly and through the configured
proxy. It cannot supply a keybox and is excluded from the default `SOURCES` in
`action.sh`. Decoder support remains for manual use if a usable provider becomes
available; an empty response is always rejected. The integration tests use generated
data and mock downloads and do not establish live source availability. Duck’s manual
repository browser uses interactive selection/download tokens and CAPTCHA, so it is
not an unattended source here. Bundled AOSP keys, generated “unknown” keys, local import,
device RKP enrollment and arbitrary custom provider UI are outside this update flow.

Compatibility/source references and the retained MIT notice are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Build

Requires Go, Node.js/npm and zip. UPX is optional; without it the package uses the
uncompressed static binary.

    sh package.sh              # build everything -> dist/keybox-lite-<version>.zip
    # or: make package

Individual build steps:

    sh build.sh                # Go binary + optional UPX
    (cd webui && npm ci && npm run build)  # WebUI -> webroot/

Packaging includes only module runtime files. The source icon lives at
`webui/public/icon.png`; generated binaries, WebUI output and archives are ignored.

## Development checks

    (cd src && go test ./... && go vet ./...)
    sh -n action.sh sources.sh settings.sh service.sh customize.sh uninstall.sh build.sh package.sh
    sh tests/lock-compatibility.sh
    # Also run with mksh when changing shell descriptor/locking behavior:
    # mksh tests/lock-compatibility.sh
    sh tests/runtime-regression.sh
    sh tests/sources-regression.sh
    sh tests/standalone-integration.sh
    (cd webui && npm run check && npm test && npm run build)

See [AGENTS.md](AGENTS.md) for architecture, compatibility and validation guidelines.
Android installation, boot scheduling and the real root-manager bridge still require
device testing.

## WebUI

Shows the installed keybox (path, size, serials, XML structural validity), each certificate’s
valid-from / expiry dates and time-validity status, one-tap update and certificate checking,
an auto-update schedule (off / daily / weekly) and the module's update
records. Calls the shell through the official `kernelsu` bridge using `spawn` (not `exec`)
so a long command never freezes the WebView. Supports light/dark and English/Chinese.

Certificate time validity is checked locally against the device clock, including every
certificate in the keybox. Expired or not-yet-valid certificates stop installation;
malformed or missing validity information is reported as unknown and also stops installation.
This is separate from online revocation checking and does not verify signatures or trust chains.
`info` includes `validity.status` and per-certificate `serial`, `not_before`, `not_after`
(Unix seconds), and `status`. `check` / `verify` return 0 on success, 1 for revocation,
2 when checking fails, and 3 for certificates outside their validity period.
Certificate serials exposed by the CLI/JSON remain decimal; revocation matching converts
them to Google’s hexadecimal status-list keys. A stale mirror can confirm a listed
revocation, but absence from that mirror returns unknown (exit 2), never “not revoked”.
