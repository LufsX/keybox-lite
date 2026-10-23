import { compareAppNames } from "./appSort"

export const modes = ["auto", "generate", "hack"] as const
export type TargetMode = typeof modes[number]
export const policyKeys = ["os_patch", "vendor_patch", "boot_patch"] as const
export type PolicyKey = typeof policyKeys[number]
export type Policy = Record<string, string>
export interface Target { package_name: string; mode: TargetMode }
export interface TargetPackage extends Target { system: boolean; selected: boolean; installed: boolean }
export interface TargetDraft {
  targets: Target[]
  default_policy: Policy
  per_app_policy: Record<string, Policy>
  system_apps: string[]
  auto_add_new_apps: boolean
}
export interface TargetStatus {
  supported: boolean
  backend: { backend: string; identity: string; active: boolean; name?: string; version?: string; module_id?: string } | null
  config: Pick<TargetDraft, "targets" | "default_policy" | "per_app_policy">
  packages: TargetPackage[]
  system_apps: string[]
  auto_add_new_apps: boolean
  entry: { enabled: boolean; module_id: string | null; linked: boolean; has_own_webui: boolean }
  revision: string
  config_error?: string
  packages_error?: string
}

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value)
}
export function validPackage(value: unknown): value is string {
  if (typeof value !== "string" || value.length > 255) return false
  if (/^uid:[0-9]+$/.test(value)) return Number(value.slice(4)) <= 0xffffffff
  const [name, user, ...extra] = value.split("@")
  return !extra.length && !["target", "default_policy", "constructor", "prototype", "__proto__"].includes(name) &&
    /^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$/.test(name) &&
    (user === undefined || (/^[0-9]+$/.test(user) && Number(user) <= 0xffffffff))
}
export function validPolicyValue(key: PolicyKey, value: unknown): value is string {
  if (value === "" || value === "prop" || value === "no") return true
  if (typeof value !== "string" || !(key === "os_patch" ? /^\d{6}$/ : /^\d{8}$/).test(value)) return false
  const year = Number(value.slice(0, 4)), month = Number(value.slice(4, 6))
  if (year < 1900 || month < 1 || month > 12) return false
  if (key === "os_patch") return true
  const day = Number(value.slice(6, 8))
  return day >= 1 && day <= new Date(Date.UTC(year, month, 0)).getUTCDate()
}
export function validPolicy(value: unknown): value is Policy {
  return object(value) && Object.entries(value).every(([key, item]) =>
    (/^[A-Za-z][A-Za-z0-9_]*$/.test(key) && !["constructor", "prototype", "__proto__"].includes(key)) &&
    (policyKeys.includes(key as PolicyKey) ? validPolicyValue(key as PolicyKey, item) :
      typeof item === "string" && item.length <= 4096 && !/[\x00-\x1f\x7f]/.test(item)))
}
function validTarget(value: unknown): value is Target {
  return object(value) && validPackage(value.package_name) && modes.includes(value.mode as TargetMode)
}
function uniqueNames(values: { package_name: string }[]): boolean {
  return new Set(values.map(value => value.package_name)).size === values.length
}
export function validDraft(value: unknown): value is TargetDraft {
  if (!object(value) || !Array.isArray(value.targets) || !value.targets.every(validTarget) || !uniqueNames(value.targets) ||
      !validPolicy(value.default_policy) || !object(value.per_app_policy) ||
      !Object.entries(value.per_app_policy).every(([name, policy]) => validPackage(name) && validPolicy(policy)) ||
      !Array.isArray(value.system_apps) || !value.system_apps.every(item => validPackage(item) && !item.startsWith("uid:")) ||
      new Set(value.system_apps).size !== value.system_apps.length || typeof value.auto_add_new_apps !== "boolean") return false
  return true
}
export function parseTargetStatus(raw: string): TargetStatus {
  const value: unknown = JSON.parse(raw)
  if (!object(value) || typeof value.supported !== "boolean" ||
      !(value.backend === null || (object(value.backend) && typeof value.backend.backend === "string" &&
        typeof value.backend.identity === "string" && typeof value.backend.active === "boolean" &&
        ["name", "version", "module_id"].every(key => value.backend !== null && object(value.backend) &&
          (value.backend[key] === undefined || typeof value.backend[key] === "string")))) ||
      !object(value.config) || !validDraft({ ...value.config, system_apps: value.system_apps, auto_add_new_apps: value.auto_add_new_apps }) ||
      !Array.isArray(value.packages) || !value.packages.every(item => validTarget(item) && object(item) &&
        typeof item.system === "boolean" && typeof item.selected === "boolean" && typeof item.installed === "boolean") ||
      !uniqueNames(value.packages) || !object(value.entry) || typeof value.entry.enabled !== "boolean" ||
      !(value.entry.module_id === null || typeof value.entry.module_id === "string") ||
      typeof value.entry.linked !== "boolean" || typeof value.entry.has_own_webui !== "boolean" ||
      typeof value.revision !== "string" || !value.revision.trim() ||
      (value.config_error !== undefined && typeof value.config_error !== "string") ||
      (value.packages_error !== undefined && typeof value.packages_error !== "string")) throw new Error("Invalid targets status JSON")
  return value as unknown as TargetStatus
}
export function parseTargetAck(raw: string): void {
  const value: unknown = JSON.parse(raw)
  if (!object(value) || value.ok !== true || (value.error !== undefined && value.error !== "" && value.error !== null))
    throw new Error("Invalid targets acknowledgement JSON")
}
export function draftFromStatus(value: TargetStatus): TargetDraft {
  return JSON.parse(JSON.stringify({ ...value.config, system_apps: value.system_apps, auto_add_new_apps: value.auto_add_new_apps }))
}
export function shellQuote(value: string): string {
  if (value.includes("\0")) throw new Error("Invalid shell argument")
  return "'" + value.replace(/'/g, "'\"'\"'") + "'"
}
export function targetStatusCommand(bin: string): string { return shellQuote(bin) + " targets status" }
export function targetSaveCommand(bin: string, revision: string, draft: TargetDraft): string {
  if (!validDraft(draft) || !revision.trim()) throw new Error("Invalid targets configuration")
  return "printf %s " + shellQuote(JSON.stringify({ revision, ...draft })) + " | " + shellQuote(bin) + " targets save"
}
export function targetEntryCommand(bin: string, enabled: boolean): string {
  return shellQuote(bin) + " targets entry set " + (enabled ? "on" : "off")
}
export function visiblePackages(status: TargetStatus, draft: TargetDraft, query: string, filter = "listed", labels: ReadonlyMap<string, { label: string }> = new Map(), pinned?: ReadonlySet<string>): TargetPackage[] {
  const selected = new Map(draft.targets.map(item => [item.package_name, item.mode]))
  const known = new Set(status.packages.map(item => item.package_name))
  const system = new Set(draft.system_apps)
  const packages = status.packages.map(item => ({ ...item, selected: selected.has(item.package_name), mode: selected.get(item.package_name) ?? item.mode }))
  for (const target of draft.targets) {
    if (!known.has(target.package_name)) {
      packages.push({ ...target, system: system.has(target.package_name), installed: false, selected: true })
      known.add(target.package_name)
    }
  }
  const search = query.trim().toLowerCase()
  return packages.filter(item => (item.package_name.toLowerCase().includes(search) || (labels.get(item.package_name)?.label ?? "").toLowerCase().includes(search)) &&
    (filter === "listed" ? !item.system || system.has(item.package_name) || item.selected :
      filter === "all" || (filter === "selected" ? item.selected : filter === "system" ? item.system : !item.system)))
    .sort((a, b) => Number(pinned?.has(b.package_name) ?? b.selected) - Number(pinned?.has(a.package_name) ?? a.selected) || compareAppNames(labels.get(a.package_name)?.label || a.package_name, labels.get(b.package_name)?.label || b.package_name) || a.package_name.localeCompare(b.package_name))
}
