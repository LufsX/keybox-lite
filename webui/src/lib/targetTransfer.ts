import { validDraft, policyKeys, type TargetDraft, type TargetStatus, type Policy } from "./targets"

export const targetTransferFormat = "keybox-lite.targets"
export const targetTransferLimit = 1024 * 1024
const backendIdentities = {
  "tricky-store": "TS", "tricky-store-legacy": "TS-L", "tee-simulator-rs": "TEES-RS",
  "tee-simulator": "TEES", "oh-my-keymint": "OMK",
} as const
export type TransferBackend = { backend: string; identity: string }
export type TargetTransfer = TargetDraft & { format: typeof targetTransferFormat; version: 1; backend: TransferBackend }
export type TransferErrorCode = "size" | "json" | "format" | "version" | "data"
export class TargetTransferError extends Error {
  constructor(public code: TransferErrorCode) { super(code) }
}
export type TransferWarning = { code: "modes" | "policies" | "appPolicies" | "identifiers" | "opaque"; count: number; names: string[] }
export interface TransferPreview {
  draft: TargetDraft
  source: TransferBackend
  destination: TransferBackend
  warnings: TransferWarning[]
  missing: string[]
  changes: { added: number; removed: number; modes: number; policies: number; system: number; auto: number; order: number }
}
const fields = ["targets", "default_policy", "per_app_policy", "system_apps", "auto_add_new_apps"]
const forbidden = new Set(["__proto__", "prototype", "constructor"])
const own = (value: object, key: string) => Object.prototype.hasOwnProperty.call(value, key)
function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value) &&
    [Object.prototype, null].includes(Object.getPrototypeOf(value))
}
function keys(value: Record<string, unknown>, expected: string[]) {
  return Object.keys(value).length === expected.length && expected.every(key => own(value, key))
}
function safeTree(value: unknown, depth = 0): boolean {
  if (depth > 8) return false
  if (Array.isArray(value)) return value.length <= 10000 && value.every(item => safeTree(item, depth + 1))
  if (object(value)) return Object.keys(value).length <= 10000 &&
    Object.entries(value).every(([key, item]) => !forbidden.has(key) && key.length <= 255 && safeTree(item, depth + 1))
  return typeof value !== "string" || value.length <= 4096
}
function validBackend(value: unknown): value is TransferBackend {
  return object(value) && keys(value, ["backend", "identity"]) && typeof value.backend === "string" &&
    own(backendIdentities, value.backend) && backendIdentities[value.backend as keyof typeof backendIdentities] === value.identity
}
function nameKey(name: string): string {
  if (name.startsWith("uid:")) return "uid:" + Number(name.slice(4))
  const [base, user] = name.split("@")
  return user === undefined ? base : base + "@" + Number(user)
}
function unique(names: string[]) { return new Set(names.map(nameKey)).size === names.length }
function editablePolicy(policy: Policy): Policy {
  return Object.fromEntries(Object.entries(policy).filter(([key]) => policyKeys.includes(key as typeof policyKeys[number])))
}
function opaquePolicy(policy: Policy): Policy {
  return Object.fromEntries(Object.entries(policy).filter(([key]) => !policyKeys.includes(key as typeof policyKeys[number])))
}
function editableDraft(draft: TargetDraft): TargetDraft {
  return {
    targets: draft.targets.map(item => ({ package_name: item.package_name, mode: item.mode })),
    default_policy: editablePolicy(draft.default_policy),
    per_app_policy: Object.fromEntries(Object.entries(draft.per_app_policy).map(([name, policy]) => [name, editablePolicy(policy)]).filter(([, policy]) => Object.keys(policy).length)),
    system_apps: [...draft.system_apps], auto_add_new_apps: draft.auto_add_new_apps,
  }
}
export function exportTargetTransfer(draft: TargetDraft, backend: TransferBackend): string {
  if (!validDraft(draft) || !validBackend({ backend: backend.backend, identity: backend.identity })) throw new TargetTransferError("data")
  const value: TargetTransfer = { format: targetTransferFormat, version: 1,
    backend: { backend: backend.backend, identity: backend.identity }, ...editableDraft(draft) }
  const text = JSON.stringify(value)
  if (new TextEncoder().encode(text).byteLength > targetTransferLimit) throw new TargetTransferError("size")
  return text
}
function duplicateKeys(text: string): boolean {
  const tokens = text.match(/"(?:\\[\s\S]|[^"\\])*"|[{}\[\]:]/g) ?? []
  const stack: (Set<string> | null)[] = []
  for (let i = 0; i < tokens.length; i++) {
    const token = tokens[i]
    if (token === "{") stack.push(new Set())
    else if (token === "[") stack.push(null)
    else if (token === "}" || token === "]") stack.pop()
    else if (tokens[i + 1] === ":" && token.startsWith('"')) {
      const key = JSON.parse(token), scope = stack[stack.length - 1]
      if (scope?.has(key)) return true
      scope?.add(key)
    }
    if (stack.length > 8) return true
  }
  return false
}
export function parseTargetTransfer(text: string, current?: TargetDraft): TargetTransfer {
  if (new TextEncoder().encode(text).byteLength > targetTransferLimit) throw new TargetTransferError("size")
  let value: unknown
  try { value = JSON.parse(text) } catch { throw new TargetTransferError("json") }
  if (!object(value) || value.format !== targetTransferFormat) throw new TargetTransferError("format")
  if (value.version !== 1) throw new TargetTransferError("version")
  if (duplicateKeys(text) || !safeTree(value) || !keys(value, ["format", "version", "backend", ...fields]) ||
      !validBackend(value.backend) || !validDraft(value)) throw new TargetTransferError("data")
  if (!value.targets.every(item => keys(item as unknown as Record<string, unknown>, ["package_name", "mode"])) ||
      !unique(value.targets.map(item => item.package_name)) || !unique(value.system_apps) ||
      !unique(Object.keys(value.per_app_policy))) throw new TargetTransferError("data")
  // Opaque native policies can only be carried through unchanged at their existing location.
  const checkPolicy = (policy: Policy, existing: Policy = {}) => Object.entries(policy).every(([key, item]) =>
    policyKeys.includes(key as typeof policyKeys[number]) || (own(existing, key) && existing[key] === item))
  if (!checkPolicy(value.default_policy, current?.default_policy) ||
      !Object.entries(value.per_app_policy).every(([name, policy]) => checkPolicy(policy, current?.per_app_policy[name]))) throw new TargetTransferError("data")
  return value as unknown as TargetTransfer
}
export async function readTargetTransfer(file: Pick<File, "size" | "text">, current?: TargetDraft): Promise<TargetTransfer> {
  if (!Number.isSafeInteger(file.size) || file.size < 0 || file.size > targetTransferLimit) throw new TargetTransferError("size")
  return parseTargetTransfer(await file.text(), current)
}
export function transferCapabilities(backend: TransferBackend) {
  const nativeList = ["tricky-store", "tricky-store-legacy", "tee-simulator-rs"].includes(backend.backend)
  return { modes: nativeList, globalPolicy: nativeList,
    // All current adapters use the same Go validName contract for UID/user tokens.
    appPolicy: backend.backend === "tricky-store", uid: true, user: true }
}
const policyDifference = (before: Policy = {}, after: Policy = {}) => [...new Set([...Object.keys(before), ...Object.keys(after)])]
  .filter(key => before[key] !== after[key]).length
export function previewTargetTransfer(value: TargetTransfer, current: TargetDraft, status: TargetStatus): TransferPreview {
  if (!status.backend || !status.supported || !status.backend.active) throw new TargetTransferError("data")
  const destination = { backend: status.backend.backend, identity: status.backend.identity }
  if (!validBackend(destination)) throw new TargetTransferError("data")
  const capabilities = transferCapabilities(destination)
  const warnings: TransferWarning[] = []
  const draft = editableDraft(value)
  const identifiers = [...new Set([...draft.targets.map(item => item.package_name), ...Object.keys(draft.per_app_policy)])]
    .filter(name => (name.startsWith("uid:") && !capabilities.uid) || (name.includes("@") && !capabilities.user))
  if (identifiers.length) {
    warnings.push({ code: "identifiers", count: identifiers.length, names: identifiers })
    const excluded = new Set(identifiers)
    draft.targets = draft.targets.filter(item => !excluded.has(item.package_name))
    draft.per_app_policy = Object.fromEntries(Object.entries(draft.per_app_policy).filter(([name]) => !excluded.has(name)))
  }
  const modeNames = draft.targets.filter(item => item.mode !== "auto").map(item => item.package_name)
  if (!capabilities.modes && modeNames.length) {
    warnings.push({ code: "modes", count: modeNames.length, names: modeNames })
    draft.targets = draft.targets.map(item => ({ ...item, mode: "auto" }))
  }
  const appPolicyCount = Object.values(draft.per_app_policy).reduce((sum, policy) => sum + Object.keys(policy).length, 0)
  if (!capabilities.appPolicy && appPolicyCount) warnings.push({
    code: capabilities.globalPolicy ? "appPolicies" : "policies",
    count: appPolicyCount + (capabilities.globalPolicy ? 0 : Object.keys(draft.default_policy).length),
    names: Object.keys(draft.per_app_policy),
  })
  else if (!capabilities.globalPolicy && Object.keys(draft.default_policy).length)
    warnings.push({ code: "policies", count: Object.keys(draft.default_policy).length, names: [] })
  if (!capabilities.globalPolicy) draft.default_policy = { ...current.default_policy }
  else draft.default_policy = { ...opaquePolicy(current.default_policy), ...draft.default_policy }
  if (!capabilities.appPolicy) draft.per_app_policy = {}
  else for (const [name, policy] of Object.entries(current.per_app_policy)) {
    if (Object.keys(opaquePolicy(policy)).length) draft.per_app_policy[name] = { ...opaquePolicy(policy), ...draft.per_app_policy[name] }
  }
  const opaqueCount = Object.keys(opaquePolicy(current.default_policy)).length +
    Object.values(current.per_app_policy).reduce((sum, policy) => sum + Object.keys(opaquePolicy(policy)).length, 0)
  if (opaqueCount) warnings.push({ code: "opaque", count: opaqueCount, names: [] })
  const oldTargets = new Map(current.targets.map(item => [item.package_name, item.mode]))
  const newTargets = new Map(draft.targets.map(item => [item.package_name, item.mode]))
  const oldSystem = new Set(current.system_apps), newSystem = new Set(draft.system_apps)
  const installed = new Set(status.packages.filter(item => item.installed).map(item => nameKey(item.package_name)))
  const changes = {
    added: draft.targets.filter(item => !oldTargets.has(item.package_name)).length,
    removed: current.targets.filter(item => !newTargets.has(item.package_name)).length,
    modes: draft.targets.filter(item => oldTargets.has(item.package_name) && oldTargets.get(item.package_name) !== item.mode).length,
    policies: policyDifference(current.default_policy, draft.default_policy) +
      [...new Set([...Object.keys(current.per_app_policy), ...Object.keys(draft.per_app_policy)])]
        .reduce((sum, name) => sum + policyDifference(current.per_app_policy[name], draft.per_app_policy[name]), 0),
    system: [...new Set([...oldSystem, ...newSystem])].filter(name => oldSystem.has(name) !== newSystem.has(name)).length,
    auto: Number(current.auto_add_new_apps !== draft.auto_add_new_apps),
    order: Number(JSON.stringify(current.targets.map(item => item.package_name).filter(name => newTargets.has(name))) !==
      JSON.stringify(draft.targets.map(item => item.package_name).filter(name => oldTargets.has(name)))),
  }
  if (!validDraft(draft)) throw new TargetTransferError("data")
  return { draft, source: value.backend, destination, warnings,
    missing: [...new Set([...draft.targets.map(item => item.package_name), ...draft.system_apps, ...Object.keys(draft.per_app_policy)])]
      .filter(name => !name.startsWith("uid:") && !installed.has(nameKey(name))), changes }
}
export function targetTransferError(error: unknown): TransferErrorCode {
  return error instanceof TargetTransferError ? error.code : "data"
}
