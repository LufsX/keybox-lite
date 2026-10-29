# Keybox Lite

A KernelSU / APatch / Magisk module that updates the Tricky Store / TEESimulator
/ TEESimulator-RS / OhMyKeymint keybox from the module Action button or the built-in WebUI.

- **Sources**: MeowDump "Megatron" and dpejoh "spectre", raced across direct and proxy routes.
  Tricky Addon decoding is opt-in and excluded from automatic updates. Spectre tries
  each eligible candidate through cleanup, backend validation and certificate checks
  before accepting it.
- **Decoding / ASN.1 / revocation check** run in a Go binary (`bin/keyboxlite`, static arm64).
  Semantics: base64 x10 + hex + ROT13, shuffled base64, X.509 serial extraction, Google
  attestation status matching against the official Google list, racing direct access and the configured proxy.
- **Runtime** runs in Go: source selection, downloads, preparation, verification, backed-up
  installation, settings, scheduling and cleanup. Downloads use curl/wget through `os/exec`.
- **Install** runs entirely within Keybox Lite. Duck ToolBox is not required and
  is never invoked, even if installed. Backend rules and paths follow its supported
  implementations. Every install backs up the old file and atomically replaces it,
  preserving its owner and mode; a new file uses mode 0644.
  An incompatible keybox or unavailable revocation check stops that candidate.
- **Runtime locking** uses the kernel's `flock` syscall directly; an external `flock`
  command is not required.
- **TS target management** supports TS / TS-L app selection, generation / hack modes,
  patch policies and adding newly observed user apps at boot. An optional WebUI entry
  links into the active backend without replacing an existing native WebUI.

## Layout

    action.sh      thin Action-button entry to the Go update supervisor
    settings.sh    thin entry to Go settings (schedule and source)
    service.sh     thin boot entry to target maintenance and the Go scheduler
    webui.sh       standalone WebUI launcher for injected Magisk Action entries
    customize.sh   installer
    uninstall.sh
    module.prop
    bin/keyboxlite Go binary (built, not tracked)
    src/           Go source (backend, targets, runtime, decoding and certificate checks)
    webui/         Svelte 5 + Tailwind v4 + TS + lucide-svelte WebUI
    webroot/       WebUI build output (bundled into the zip)
    META-INF/      module installer

## Standalone backend compatibility

| Backend | Detection | Keybox destination | Additional keybox rules |
| --- | --- | --- | --- |
| Tricky Store | `tricky_store`, versionCode >= 246 | `/data/adb/tricky_store/keybox.xml` | At least one certified key |
| Tricky Store legacy | `tricky_store`, lower/missing versionCode | Same as above | At least one certified key |
| TEESimulator-RS | `tricky_store` with module name `TEESimulator-RS` or `libcertgen.so` | Same as Tricky Store | At least one certified key |
| OhMyKeymint | `oh_my_keymint` | `/data/misc/keystore/omk/keybox.xml` | EC key required; each key needs a chain and correct declared certificate count |
| TEESimulator | `teesim` | `/data/adb/teesim/` + `profiles.default.keybox` from `config.json` (default `keybox.xml`) | EC and RSA keys required; every key needs at least two certificates |

TEESimulator-RS keeps Tricky Store's module ID and target.txt layout while raising its
versionCode past Tricky Store's config.ini boundary, so the module name and its native
certificate generator take precedence over versionCode.

Detection uses installed modules with `module.prop`, preferring active Tricky Store,
then OhMyKeymint, then TEESimulator. Disabled/removal-pending modules are shown for
diagnostics but never installed into. Staged-only modules are not active. An unsafe
or malformed configuration is an error, not a reason to guess a different target.
Only TEESimulator’s default profile is updated; per-app custom profiles are not rewritten.

Target management edits each backend’s own app list: Tricky Store’s `config.ini` /
`target.txt`, TEESimulator-RS `target.txt`, TEESimulator `config.json`
(`profiles.default.apps`) and OhMyKeymint `injector.toml` (`scoop`). Modes and
patch policies exist only in the Tricky Store formats, so they are hidden for the other
backends. A TEESimulator-RS `target.txt` that declares keybox scopes, or a package-scoped
`security_patch.txt`, is reported as unreadable instead of being rewritten without those
scopes. TEESimulator and OhMyKeymint need their daemon configuration to exist; a missing
file is an error, not a reason to write a partial one.

Before installation the XML is cleaned of comments/BOM/zero-width markers, structurally
validated, and checked against the selected backend. Keybox Lite checks PEM/DER shape
and key/leaf-certificate algorithm compatibility. Verification checks private-key
arithmetic, key/certificate correspondence, certificate signatures and X.509 constraints.
Root recognition is reported separately; unknown and AOSP roots produce a warning.
The backend and target are rechecked before replacement. No daemon is restarted
automatically, matching Duck’s keybox installation behavior.

Diagnostic commands:

    bin/keyboxlite backend                     # selected and installed backends as JSON
    bin/keyboxlite backend id                  # active backend ID
    bin/keyboxlite backend target              # safe active destination
    bin/keyboxlite prepare tricky-store < candidate.xml > prepared.xml
    bin/keyboxlite targets status              # TS targets, packages and entry state
    bin/keyboxlite targets save < targets.json # save against the status revision
    bin/keyboxlite targets entry set on        # attach the optional WebUI entry
    bin/keyboxlite targets entry set off       # remove only this module's links

`backend`, `info` and `targets` accept `--root <directory>` for isolated host fixtures.
The production update, schedule and uninstall commands do not accept a host sysroot.

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

Source downloads race direct access and configured proxies with at most six active
routes. A confirmed revocation rejects every route of that source. For each URL,
failed curl attempts fall back to BusyBox wget and then standalone wget; logs show
which downloader ran and its error. Partial or empty failed responses are rejected.

Spectre prefers clean entries, then softbanned entries, excluding revoked entries and
ignoring unverified `working`/`autoOverride` hints. Download, decode or validation
failure advances to the next candidate. Catalog flags never replace certificate checks.
The downloader/decoder/parser limit source files to 1 MiB, including streamed wget
output; all candidates require current time validity and a usable revocation list.

The Addon URL and format match Duck’s default provider. Addon is excluded from the
default sources; manual use requires `KBL_SOURCES="meow spectre addon" sh action.sh`.
The saved source selection (`auto`, `meow` or `spectre`, stored in
`/data/adb/keybox-lite/config`) applies to Action, WebUI and scheduled updates and
defaults to `auto`; `KBL_SOURCES` overrides it for a single run.
The optional “Skip revocation check” setting defaults to off and applies to updates;
when enabled, certificate dates, private keys and chains are still validated, and
revocation is logged as skipped. The manual Check button retains revocation checking.
`KB_ACCEL` overrides the default `https://c.etc.moe/` proxy prefix; an empty value
selects direct and built-in GitHub routes.
Empty responses are rejected. Integration tests use generated data and mock downloads.
Duck’s manual repository browser requires interactive selection/download tokens and CAPTCHA.

Target management follows [Duck ToolBox](https://github.com/eltavine/Duck-ToolBox),
whose Tricky Store manager originates from KOWX712's [Tricky Addon: Update Target List](https://github.com/KOWX712/Tricky-Addon-Update-Target-List)
(Apache-2.0).

Build dependencies are Go, Node.js/npm and zip. UPX compression is optional;
without UPX, the module contains the uncompressed static binary. Archives contain
module runtime files. The source icon is `webui/public/icon.png`; generated binaries,
WebUI output and archives are ignored by Git.

## Development checks

Go tests are stored in `src/test/`. The runner uses Go’s build overlay to run them in their original packages; package arguments are relative to `src`. Run the complete suite through the runner because plain `go test ./...` skips the separate test module.

    sh src/test/run.sh test ./...
    sh src/test/run.sh vet ./...
    sh -n action.sh settings.sh service.sh webui.sh customize.sh uninstall.sh build.sh package.sh
    sh tests/lock-compatibility.sh
    # Wrapper compatibility with the Android shell:
    # mksh tests/lock-compatibility.sh
    sh tests/runtime-regression.sh
    sh tests/sources-regression.sh
    sh tests/source-race-regression.sh
    sh tests/standalone-integration.sh
    (cd webui && npm run check && npm test && npm run build)

See [AGENTS.md](AGENTS.md) for architecture, compatibility and validation guidelines.

## WebUI

Shows the installed keybox (path, size, serials, XML structural validity), each certificate’s
valid-from / expiry dates and time-validity status, one-tap update and certificate checking,
an auto-update schedule (off / daily / weekly), a keybox source selector
(automatic / Meow / Spectre) and the module's update records. The TS / TS-L targets panel provides app search, user/system filters, manual
package or UID entries, auto / generate / hack modes, global patch policies and
per-app policies on modern TS. Saves preserve unknown backend configuration and
reject stale revisions. Automatic target additions run at boot and apply only to
newly observed user apps after the initial baseline.

The optional WebUI entry creates a link in the active backend's module directory.
Existing native WebUIs, actions and foreign links are retained. On Magisk, an absent
Action entry links to `webui.sh`, which opens an installed KSUWebUIStandalone or
WebUI X host. Turning the feature off or uninstalling removes only this module's
links. Boot maintenance restores links removed by backend module updates. It retries
for up to ten minutes after checking Android startup or package-manager readiness;
maintenance failures do not stop the keybox scheduler.

The WebUI calls the shell through the official `kernelsu` bridge using `spawn` (not `exec`)
so a long command never freezes the WebView. Supports light/dark and English/Chinese.

Certificate time validity is checked locally against the device clock, including every
certificate in the keybox. Expired or not-yet-valid certificates stop installation;
malformed or missing validity information is reported as unknown and also stops installation.
Local cryptographic integrity and online revocation are separate results. Root recognition
does not change the existing installation policy.
`info` includes `validity.status` and per-certificate `serial`, `not_before`, `not_after`
(Unix seconds), and `status`. `check` / `verify` return 0 on success, 1 for revocation,
2 when checking fails, and 3 for certificates outside their validity period.
Certificate serials exposed by the CLI/JSON remain decimal; revocation matching converts
them to Google’s hexadecimal status-list keys. Official direct and proxy requests race;
the first valid response wins. Only official results are cached for up to 10 minutes.
With revocation checking enabled, an unavailable official response returns unknown
(exit 2). `verify --skip-revocation <keybox>` validates locally and reports
`RESULT: skipped (revocation)` on success; it does not report the keybox as not revoked.
