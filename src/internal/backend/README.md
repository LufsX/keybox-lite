# Backend detection contract

This package independently detects the four variants supported by Duck ToolBox's
`duck-tricky-store` crate. It does not invoke Duck or any backend executable.
All filesystem operations in this package are read-only.

| Backend JSON ID | Module directory ID | Configuration | Default keybox |
| --- | --- | --- | --- |
| `tricky-store` (TS) | `tricky_store` with versionCode >= 246 | `/data/adb/tricky_store/config.ini` | `/data/adb/tricky_store/keybox.xml` |
| `tricky-store-legacy` (TS-L) | `tricky_store` with older/missing/invalid versionCode | `/data/adb/tricky_store/target.txt` and `security_patch.txt` | `/data/adb/tricky_store/keybox.xml` |
| `oh-my-keymint` (OMK) | `oh_my_keymint` | `/data/misc/keystore/omk/config.toml` and `injector.toml` | `/data/misc/keystore/omk/keybox.xml` |
| `tee-simulator` (TEES) | `teesim` | `/data/adb/teesim/config.json` | `/data/adb/teesim/<profiles.default.keybox>`, falling back to `keybox.xml` only when the configuration or setting is absent |

Installed modules must have `/data/adb/modules/<id>/module.prop`.
Selection prefers the first active module in TS, OMK, TEES order. A `disable` or
`remove` marker makes a module inactive. The installed `update` marker does not.
Modules staged only under `modules_update` are ignored. If every module is
inactive, JSON still selects the first installed module for diagnosis.

TEESimulator's configured keybox must be a nonempty relative path with no empty,
dot or parent components, backslashes or control characters. Existing path
components must not be symlinks, and an existing final target must be a regular
file. Malformed JSON, invalid setting types and unsafe paths produce an error
and an empty `keybox_path`; they never cause installation into a guessed
default.
Only the default profile is managed; other profiles' keyboxes are left alone.

## Go and CLI

`Detect(sysroot string) (Report, error)` returns device paths; the sysroot affects
only filesystem reads. Empty sysroot means `/`. A report contains `backends`
(always an array) and `selected` (a target or null). A selected target's unsafe
configuration is included as `config_error` and also returned as an error.
An unselected backend's unsafe configuration is reported without blocking a
healthy selected backend.

`Run(args []string) int` implements:

- `backend [json] [--root <sysroot>]`: output the report as JSON. No installed
  backend is a successful empty report; an unusable selected target returns 1
  after printing the diagnostic report.
- `backend target [--root <sysroot>]`: print only the selected device keybox path.
- `backend id [--root <sysroot>]`: print only the selected backend JSON ID.

Both installation outputs reject missing, inactive or unsafe selected targets
with status 1 and empty stdout. Bad CLI arguments return 2. Success returns 0.
The caller must check command exit status and quote the returned path.

The `info` command uses the same detector and reports keybox status,
`backend` (the selected target or null), `backends`, and optional `backend_error`.
With no backend, its path is empty and exists is false. Explicit `info <keybox>`
inspection is supported;
`--root <sysroot>` enables isolated host fixtures.

## Installation and reload

Every target describes `install_mode: "atomic-replace"`,
`new_file_mode: "0644"` and `reload: "backend-managed"`. `action.sh` validates
the keybox, backs up an existing file and replaces it through a temporary sibling,
preserving existing ownership and mode. It removes staging files on failure and
rechecks path safety and active state before replacement.

The reload field means that installation does not start, kill or restart a service,
matching Duck's keybox installer.

Regression tests use temporary sysroots and synthetic XML only:

```sh
(cd src && go test ./internal/backend ./internal/info && go vet ./internal/backend ./internal/info)
```
