import { compareAppNames } from "./appSort"

export const modes = ["auto", "generate", "hack"] as const
export type TargetMode = typeof modes[number]
export const policyKeys = ["os_patch", "vendor_patch", "boot_patch"] as const
export type PolicyKey = typeof policyKeys[number]
export type Policy = Record<string, string>
export interface Target { package_name: string; mode: TargetMode }
export type AppLabel = { label: string; installedAt?: number | null }
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

type BackendLabel = { identity: string; name?: string; version?: string }
export function targetBackendTitle(backend: BackendLabel | null | undefined, fallback: string): string {
  if (!backend) return fallback
  const names: Record<string, string> = {
    TS: "Tricky Store", "TS-L": "Tricky Store", "TEES-RS": "TEESimulator-RS",
    TEES: "TEESimulator", OMK: "OhMyKeymint",
  }
  return backend.name?.trim() || (Object.prototype.hasOwnProperty.call(names, backend.identity) ? names[backend.identity] : fallback)
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
export type TargetEntry = TargetStatus["entry"]
function validEntry(value: unknown): value is TargetEntry {
  return object(value) && typeof value.enabled === "boolean" &&
    (value.module_id === null || typeof value.module_id === "string") &&
    typeof value.linked === "boolean" && typeof value.has_own_webui === "boolean"
}
export function parseTargetEntry(raw: string): TargetEntry {
  const value: unknown = JSON.parse(raw)
  if (!validEntry(value)) throw new Error("Invalid targets entry JSON")
  return value
}
export function targetEntryStatusCommand(bin: string): string { return shellQuote(bin) + " targets entry status" }
function validPackages(value: unknown): value is TargetPackage[] {
  return Array.isArray(value) && value.every(item => validTarget(item) && object(item) &&
    typeof item.system === "boolean" && typeof item.selected === "boolean" && typeof item.installed === "boolean") &&
    uniqueNames(value)
}
export function parseTargetPackages(raw: string): TargetPackage[] {
  const value: unknown = JSON.parse(raw)
  if (!validPackages(value)) throw new Error("Invalid targets packages JSON")
  return value
}
export function targetPackagesCommand(bin: string): string { return shellQuote(bin) + " targets packages" }
export function parseTargetStatus(raw: string, previousPackages?: TargetPackage[]): TargetStatus {
  const value: unknown = JSON.parse(raw)
  if (!object(value) || typeof value.supported !== "boolean" ||
      !(value.backend === null || (object(value.backend) && typeof value.backend.backend === "string" &&
        typeof value.backend.identity === "string" && typeof value.backend.active === "boolean" &&
        ["name", "version", "module_id"].every(key => value.backend !== null && object(value.backend) &&
          (value.backend[key] === undefined || typeof value.backend[key] === "string")))) ||
      !object(value.config) || !validDraft({ ...value.config, system_apps: value.system_apps, auto_add_new_apps: value.auto_add_new_apps }) ||
      (value.packages === undefined ? previousPackages === undefined : !validPackages(value.packages)) || !validEntry(value.entry) ||
      typeof value.revision !== "string" || !value.revision.trim() ||
      (value.config_error !== undefined && typeof value.config_error !== "string") ||
      (value.packages_error !== undefined && typeof value.packages_error !== "string")) throw new Error("Invalid targets status JSON")
  return { ...value, packages: value.packages ?? previousPackages } as unknown as TargetStatus
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
export function targetStatusCommand(bin: string, configOnly = false): string {
  return shellQuote(bin) + " targets status" + (configOnly ? " --config-only" : "")
}
export function targetSaveCommand(bin: string, revision: string, draft: TargetDraft): string {
  if (!validDraft(draft) || !revision.trim()) throw new Error("Invalid targets configuration")
  return "printf %s " + shellQuote(JSON.stringify({ revision, ...draft })) + " | " + shellQuote(bin) + " targets save"
}
export function targetEntryCommand(bin: string, enabled: boolean): string {
  return shellQuote(bin) + " targets entry set " + (enabled ? "on" : "off")
}
export type TargetSort = "name" | "package" | "installed"
/**
 * The directory depends on package membership, not draft selection or modes.
 * Returning the same array lets Svelte retain its prepared metadata and order.
 */
export function createTargetPackageDirectory() {
  let source: TargetPackage[] | undefined
  let sourceKey = ""
  let known = new Set<string>()
  let missingKey = ""
  let directory: TargetPackage[] = []
  return (status: TargetStatus, draft: TargetDraft): TargetPackage[] => {
    let changed = false
    if (source !== status.packages) {
      const key = JSON.stringify(status.packages.map(item => [item.package_name, item.system, item.installed, item.mode]))
      changed = key !== sourceKey
      sourceKey = key
      source = status.packages
      known = new Set(status.packages.map(item => item.package_name))
    }
    const system = new Set(draft.system_apps)
    const missing = draft.targets.filter(item => !known.has(item.package_name))
      .map(item => ({ package_name: item.package_name, system: system.has(item.package_name), installed: false, selected: false, mode: "auto" as const }))
    const key = JSON.stringify(missing.map(item => [item.package_name, item.system]))
    if (changed || key !== missingKey) {
      missingKey = key
      directory = [...status.packages, ...missing]
    }
    return directory
  }
}

/** Sort a copy once; query, filters and draft state are applied separately. */
export function orderTargetPackages(packages: readonly TargetPackage[], labels: ReadonlyMap<string, AppLabel>, sort?: TargetSort, pinned?: ReadonlySet<string>): TargetPackage[] {
  return [...packages].sort((a, b) => {
    if (sort === undefined) {
      const priority = Number(pinned?.has(b.package_name) ?? b.selected) - Number(pinned?.has(a.package_name) ?? a.selected)
      if (priority) return priority
    }
    if (sort === "installed") {
      const first = labels.get(a.package_name)?.installedAt
      const second = labels.get(b.package_name)?.installedAt
      const aTime = typeof first === "number" && Number.isFinite(first) && first > 0 ? first : null
      const bTime = typeof second === "number" && Number.isFinite(second) && second > 0 ? second : null
      if (aTime === null && bTime !== null) return 1
      if (aTime !== null && bTime === null) return -1
      if (aTime !== null && bTime !== null && aTime !== bTime) return bTime - aTime
    }
    return sort === "package" ? a.package_name.localeCompare(b.package_name) :
      compareAppNames(labels.get(a.package_name)?.label || a.package_name, labels.get(b.package_name)?.label || b.package_name) ||
      a.package_name.localeCompare(b.package_name)
  })
}

/** Project current selection/modes and filter the existing order without sorting. */
export function filterTargetPackages(packages: readonly TargetPackage[], draft: TargetDraft, query: string, filter = "listed", labels: ReadonlyMap<string, AppLabel> = new Map(), showSystem?: boolean): TargetPackage[] {
  const selected = new Map(draft.targets.map(item => [item.package_name, item.mode]))
  const system = new Set(draft.system_apps)
  const search = query.trim().toLowerCase()
  const result: TargetPackage[] = []
  for (const item of packages) {
    const isSelected = selected.has(item.package_name)
    if ((showSystem !== false || !item.system) &&
        (item.package_name.toLowerCase().includes(search) || (labels.get(item.package_name)?.label ?? "").toLowerCase().includes(search)) &&
        (filter === "listed" ? showSystem || !item.system || system.has(item.package_name) || isSelected :
          filter === "all" || (filter === "selected" ? isSelected : filter === "system" ? item.system : !item.system))) {
      result.push({ ...item, selected: isSelected, mode: selected.get(item.package_name) ?? item.mode })
    }
  }
  return result
}

export function visiblePackages(status: TargetStatus, draft: TargetDraft, query: string, filter = "listed", labels: ReadonlyMap<string, AppLabel> = new Map(), pinned?: ReadonlySet<string>, sort?: TargetSort, showSystem?: boolean): TargetPackage[] {
  const directory = createTargetPackageDirectory()(status, draft)
  const rows = filterTargetPackages(directory, draft, query, filter, labels, showSystem)
  // Omitted sorting preserves the selection grouping used by system dialogs.
  return orderTargetPackages(rows, labels, sort, pinned)
}
