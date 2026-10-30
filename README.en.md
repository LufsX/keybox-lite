# Keybox Lite

[简体中文](README.md)

Keybox Lite is a KernelSU / APatch / Magisk module. It updates the keybox of Tricky Store,
TEESimulator, TEESimulator-RS and OhMyKeymint. You can start an update from the module's
Action button or from the built-in WebUI.

## Requirements

- Android device, arm64
- KernelSU, APatch or Magisk
- `curl` or `wget` on the device

## Install

1. Download the latest zip package.
2. Flash the zip in KernelSU, APatch or Magisk.
3. Reboot the device.

## Usage

- Action button: tap the module's Action button to start an update. KernelSU and APatch
  provide this button.
- WebUI: open the module's WebUI. Tap "Update keybox".
- Auto update: select "Daily" or "Weekly" in the WebUI.
- Stop an update: tap "Stop update" in the WebUI.

## Supported backends

| Backend | Detection | Keybox path | Extra requirements |
| --- | --- | --- | --- |
| Tricky Store | module id `tricky_store` | `/data/adb/tricky_store/keybox.xml` | at least 1 certified key |
| Tricky Store legacy | module id `tricky_store`, lower or missing versionCode | same as Tricky Store | same as Tricky Store |
| TEESimulator-RS | module id `tricky_store`, module name `TEESimulator-RS` or `libcertgen.so` | same as Tricky Store | same as Tricky Store |
| OhMyKeymint | module id `oh_my_keymint` | `/data/misc/keystore/omk/keybox.xml` | an EC key is required |
| TEESimulator | module id `teesim` | `profiles.default.keybox` from `config.json`, default `/data/adb/teesim/keybox.xml` | EC and RSA keys are required |

The module checks installed modules in this order: Tricky Store, OhMyKeymint, TEESimulator.
The module does not install into disabled or removal-pending modules. When several backends are present, the module updates the selected one.
TEESimulator updates `profiles.default` only. Per-app custom profiles are not rewritten.

## Validation and installation

Before installation, the module checks 4 properties of each candidate keybox: structure,
cryptography, validity period and revocation status.

- Structure: XML structure, and the algorithm of each key and certificate.
- Cryptography: private key arithmetic, key/certificate correspondence, certificate
  signatures and X.509 constraints.
- Validity: every certificate is inside its validity period. The module uses the device clock.
- Revocation: the module queries Google's official status list.

A candidate must also satisfy the extra requirements of the selected backend (see above).
The module installs a candidate after all 4 checks pass.
If the revocation status is unavailable, the module skips that candidate.
With "Skip revocation check" enabled, the module skips the revocation status only, and runs the
other checks unchanged.

Before replacement, the module backs up the old file. The replacement is a single atomic write.
The module keeps the owner and the mode of the installed file. A new file uses mode 0644.

The module checks the backend and the keybox path again before replacement.
If a candidate fails, the module tries the next candidate.
The module does not restart the backend daemon after installation.

## WebUI

- Shows the installed keybox path, size and certificate list.
- Shows the valid-from date, the expiry date and the time validity of each certificate.
- Provides "Update keybox", "Check certificates" and "Stop update".
- Auto update: "Off", "Daily" or "Weekly".
- Source: "Automatic", "Meow" or "Spectre".
- "Target management" edits the app list, the tracking mode and the patch policies.
- Imports a local keybox file, and restores a backed-up keybox.
- Exports and imports app configuration as JSON.
- Optional WebUI entry: adds an entry for this module in the backend module directory.

## Build

`sh package.sh` builds the Go binary and the WebUI, and produces the zip package.
The build needs Go, Node.js, npm and zip.
UPX compression is optional.
The output file is `dist/keybox-lite-<version>-<versionCode>.zip`.

## License

Copyright (C) 2026 LufsX.

This project is licensed under the GNU General Public License, version 3 or later
(GPL-3.0-or-later). The full text is in [LICENSE](LICENSE).
Third-party components bundled in the WebUI are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Credits

- The target management configuration formats and the automatic tracking behaviour follow
  [Duck ToolBox](https://github.com/eltavine/Duck-ToolBox) (MIT).
- Tricky Store target management originates from KOWX712's
  [Tricky Addon: Update Target List](https://github.com/KOWX712/Tricky-Addon-Update-Target-List) (Apache-2.0).
