import { exec, type ExecResult } from "./bridge"
import type { CertificateValidity, KeyboxIntegrity, ValidityStatus } from "./certificateStatus"

export const MAX_KEYBOX_BYTES = 1024 * 1024
export const KEYBOX_CHUNK_BYTES = 24 * 1024

export interface BackendTarget {
  backend: "tricky-store" | "tricky-store-legacy" | "oh-my-keymint" | "tee-simulator" | "tee-simulator-rs"
  identity: string
  module_id: string
  module_dir: string
  active: boolean
  config_dir: string
  config_path: string
  keybox_path: string
  install_mode: string
  new_file_mode: string
  reload: string
  name?: string
  version?: string
  version_code?: number
  config_error?: string
}
export interface KeyboxPreview {
  id: string
  size: number
  modified: number
  revision: string
  structure_valid: boolean
  compatible: boolean
  serials: string[]
  validity: { status: ValidityStatus; certificates: CertificateValidity[] } | null
  integrity: KeyboxIntegrity | null
  backend: BackendTarget
  target: string
  revocation: "not_checked"
  // Raw backend errors are deliberately excluded from the presentation model.
}
export interface BackupList {
  backend: BackendTarget
  target: string
  backups: KeyboxPreview[]
}
export type FileErrorCode = "protocol" | "read" | "upload" | "empty" | "tooLarge" | "extension" | "cancelled" | "detached"
export class KeyboxFilesError extends Error {
  constructor(public readonly code: FileErrorCode) { super(code); this.name = "KeyboxFilesError" }
}
export type FileExecutor = (command: string) => Promise<ExecResult>
const fail = (code: FileErrorCode): never => { throw new KeyboxFilesError(code) }
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === "object" && !Array.isArray(value)
const timestamp = (value: unknown): value is number => typeof value === "number" && Number.isSafeInteger(value)
const integer = (value: unknown): value is number => timestamp(value) && value >= 0
const text = (value: unknown): value is string => typeof value === "string"
const validityStates = ["valid", "expired", "not_yet_valid", "unknown"]
export const isImportId = (id: string) => /^[a-f0-9]{64}$/.test(id)
const safeBasename = (value: string) => !!value && value !== "." && value !== ".." &&
  !/[\/\\\\\x00-\x1f\x7f-\x9f]/.test(value)
export function isBackupId(id: string, target?: string): boolean {
  if (!safeBasename(id)) return false
  const boundary = id.lastIndexOf(".bak.")
  if (boundary < 1 || !/^[0-9]+$/.test(id.slice(boundary + 5))) return false
  if (target === undefined) return true
  const basename = target.slice(target.lastIndexOf("/") + 1)
  return safeBasename(basename) && id.slice(0, boundary) === basename
}
export function shellQuote(value: string): string {
  if (value.includes("\u0000")) return fail("protocol")
  return "'" + value.replace(/'/g, "'\"'\"'") + "'"
}

function parseJSON(result: ExecResult): unknown {
  if (result.errno !== 0 || result.failure) return fail("protocol")
  try { return JSON.parse(result.stdout) } catch { return fail("protocol") }
}
function parseBackend(value: unknown): BackendTarget {
  if (!object(value) || !["tricky-store", "tricky-store-legacy", "oh-my-keymint", "tee-simulator", "tee-simulator-rs"].includes(String(value.backend)) ||
    !["identity", "module_id", "module_dir", "config_dir", "config_path", "keybox_path", "install_mode", "new_file_mode", "reload"].every(key => text(value[key])) ||
    typeof value.active !== "boolean" ||
    !["name", "version", "config_error"].every(key => value[key] === undefined || text(value[key])) ||
    (value.version_code !== undefined && !integer(value.version_code))) return fail("protocol")
  return { backend: value.backend as BackendTarget["backend"], identity: value.identity as string,
    module_id: value.module_id as string, module_dir: value.module_dir as string, active: value.active,
    config_dir: value.config_dir as string, config_path: value.config_path as string, keybox_path: value.keybox_path as string,
    install_mode: value.install_mode as string, new_file_mode: value.new_file_mode as string, reload: value.reload as string,
    ...(text(value.name) ? { name: value.name } : {}), ...(text(value.version) ? { version: value.version } : {}),
    ...(text(value.config_error) ? { config_error: value.config_error } : {}),
    ...(integer(value.version_code) ? { version_code: value.version_code } : {}) }
}
function parseTarget(value: unknown, backend: BackendTarget): string {
  if (!text(value) || !value.startsWith("/") || !value.slice(1).split("/").every(safeBasename) ||
    value !== backend.keybox_path) return fail("protocol")
  return value
}
export function parsePreview(value: unknown, expectedId?: string): KeyboxPreview {
  if (!object(value) || !text(value.id) ||
    (expectedId !== undefined && value.id !== expectedId) ||
    !integer(value.size) || !integer(value.modified) || !text(value.revision) ||
    // Empty revision is allowed only for unusable files in the backup list.
    !/^(?:[a-f0-9]{64})?$/.test(value.revision) ||
    typeof value.structure_valid !== "boolean" || typeof value.compatible !== "boolean" ||
    !Array.isArray(value.serials) || !value.serials.every(s => text(s) && /^[0-9]{1,256}$/.test(s))) return fail("protocol")
  const backend = parseBackend(value.backend)
  const target = parseTarget(value.target, backend)
  if (value.revocation !== "not_checked" || (!isImportId(value.id) && !isBackupId(value.id, target))) return fail("protocol")
  const validity = value.validity
  if (validity !== null && (!object(validity) || !validityStates.includes(String(validity.status)) ||
    !Array.isArray(validity.certificates) || !validity.certificates.every(c => object(c) &&
      text(c.serial) && /^[0-9]{1,256}$/.test(c.serial) &&
      timestamp(c.not_before) && timestamp(c.not_after) && validityStates.includes(String(c.status))))) return fail("protocol")
  const integrity = value.integrity
  if (integrity !== null && (!object(integrity) || !["valid", "invalid", "unknown"].includes(String(integrity.status)) ||
    !Array.isArray(integrity.keys) || !integrity.keys.every(k => object(k) &&
      ["ec", "rsa"].includes(String(k.algorithm)) && typeof k.private_key_valid === "boolean" &&
      typeof k.key_match === "boolean" && typeof k.chain_valid === "boolean" &&
      ["google", "aosp_ec", "aosp_rsa", "knox", "unknown"].includes(String(k.root))))) return fail("protocol")
  // Copy allowlisted fields so unexpected XML, PEM, paths or backend errors cannot reach the DOM.
  return { id: value.id, size: value.size, modified: value.modified, revision: value.revision,
    structure_valid: value.structure_valid, compatible: value.compatible, serials: [...value.serials],
    backend, target, revocation: "not_checked",
    validity: validity === null ? null : {
      status: validity.status as ValidityStatus,
      certificates: (validity.certificates as CertificateValidity[]).map(c => ({
        serial: c.serial, not_before: c.not_before, not_after: c.not_after, status: c.status,
      })),
    },
    integrity: integrity === null ? null : {
      status: integrity.status as KeyboxIntegrity["status"],
      keys: (integrity.keys as KeyboxIntegrity["keys"]).map(k => ({
        algorithm: k.algorithm, private_key_valid: k.private_key_valid, key_match: k.key_match,
        chain_valid: k.chain_valid, root: k.root,
      })),
    } }
}
export function canConfirm(preview: KeyboxPreview | null): boolean {
  return !!preview && preview.size > 0 && preview.size <= MAX_KEYBOX_BYTES && !!preview.revision &&
    preview.structure_valid && preview.compatible && preview.serials.length > 0 &&
    preview.validity?.status === "valid" && preview.validity.certificates.length > 0 &&
    preview.validity.certificates.every(c => c.status === "valid") &&
    preview.integrity?.status === "valid" && preview.integrity.keys.length > 0 &&
    preview.integrity.keys.every(k => k.private_key_valid && k.key_match && k.chain_valid)
}
function checkAbort(signal: AbortSignal) { if (signal.aborted) fail("cancelled") }
export interface PreparedImport {
  preview: KeyboxPreview
  discard: () => Promise<void>
}
export class KeyboxFilesClient {
  private readonly command: string
  constructor(bin: string, moddir: string, private readonly execute: FileExecutor = exec) {
    this.command = shellQuote(bin) + " keybox-files " + shellQuote(moddir)
  }
  private async request(args: string): Promise<unknown> {
    try { return parseJSON(await this.execute(this.command + " " + args)) }
    catch { return fail("protocol") }
  }
  async backups(): Promise<BackupList> {
    const value = await this.request("backups")
    if (!object(value) || !Array.isArray(value.backups)) return fail("protocol")
    const backend = parseBackend(value.backend)
    const target = parseTarget(value.target, backend)
    const backups = value.backups.map(v => parsePreview(v))
    if (backups.some(v => !isBackupId(v.id, target) || v.target !== target ||
      JSON.stringify(v.backend) !== JSON.stringify(backend)) ||
      new Set(backups.map(v => v.id)).size !== backups.length) return fail("protocol")
    return { backend, target, backups }
  }
  async inspect(id: string, target?: string): Promise<KeyboxPreview> {
    if (!isImportId(id) && (!target || !isBackupId(id, target))) return fail("protocol")
    const preview = parsePreview(await this.request("inspect " + shellQuote(id)), id)
    if (target !== undefined && preview.target !== target) return fail("protocol")
    return preview
  }
  async discard(id: string): Promise<void> {
    if (!isImportId(id)) return
    // Cleanup is idempotent and best effort; never expose the bridge's raw error.
    try { await this.execute(this.command + " discard " + shellQuote(id)) } catch { /* no output */ }
  }
  async upload(file: Pick<File, "name" | "size" | "arrayBuffer">, signal: AbortSignal): Promise<PreparedImport> {
    if (!/\.xml$/i.test(file.name)) return fail("extension")
    if (!integer(file.size) || file.size === 0) return fail("empty")
    if (file.size > MAX_KEYBOX_BYTES) return fail("tooLarge")
    let id = ""
    let bytes: Uint8Array | undefined
    try {
      checkAbort(signal)
      try { bytes = new Uint8Array(await file.arrayBuffer()) } catch { return fail("read") }
      checkAbort(signal)
      if (bytes.byteLength !== file.size) return fail("read")
      // Do not abort native create/append calls: wait for their completion to learn
      // late session IDs and finish writes before discarding the session.
      const created = await this.request("create")
      if (!object(created) || !text(created.id) || !isImportId(created.id)) return fail("upload")
      id = created.id
      checkAbort(signal)
      for (let offset = 0; offset < bytes.length; offset += KEYBOX_CHUNK_BYTES) {
        checkAbort(signal)
        const chunk = bytes.subarray(offset, Math.min(offset + KEYBOX_CHUNK_BYTES, bytes.length))
        const base64 = btoa(String.fromCharCode(...chunk))
        // File bytes never become shell syntax, output listeners, or error messages.
        let appended: unknown
        try {
          appended = parseJSON(await this.execute("printf %s " + shellQuote(base64) + " | " +
            this.command + " append " + shellQuote(id) + " " + offset))
        } catch { return fail("upload") }
        checkAbort(signal)
        if (!object(appended) || appended.ok !== true || appended.size !== offset + chunk.length) return fail("upload")
      }
      bytes.fill(0)
      bytes = undefined
      checkAbort(signal)
      const preview = await this.inspect(id)
      checkAbort(signal)
      let discarded: Promise<void> | undefined
      return { preview, discard: () => discarded ??= this.discard(id) }
    } catch (error) {
      if (id) await this.discard(id)
      if (error instanceof KeyboxFilesError && ["cancelled", "read"].includes(error.code)) throw error
      return fail("upload")
    } finally {
      bytes?.fill(0)
    }
  }
}
