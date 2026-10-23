import { getPackagesInfo } from "kernelsu"

export interface AppInfo { label: string; icon: string | null }

function androidPackage(value: string): string | null {
  const name = value.split("@")[0]
  return /^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$/.test(name) ? name : null
}

// Native metadata calls are synchronous. Yield between small batches so the
// WebView can paint and cancel a request when its page is left.
export async function packagesInfo(names: string[], signal?: AbortSignal): Promise<Map<string, AppInfo>> {
  const result = new Map<string, AppInfo>()
  const host = typeof window === "undefined" ? undefined :
    (window as unknown as { ksu?: { getPackagesInfo?: unknown; listPackages?: unknown } }).ksu
  if (!host || typeof host.getPackagesInfo !== "function") return result
  const requested = new Map<string, string[]>()
  for (const name of names) {
    const base = androidPackage(name)
    if (base) requested.set(base, [...(requested.get(base) ?? []), name])
  }
  const packages = [...requested.keys()]
  for (let offset = 0; offset < packages.length; offset += 24) {
    if (signal?.aborted) return result
    await new Promise<void>(resolve => setTimeout(resolve, 0))
    if (signal?.aborted) return result
    const batch = packages.slice(offset, offset + 24)
    try {
      const raw: unknown = await getPackagesInfo(batch)
      if (signal?.aborted) return result
      const infos = Array.isArray(raw) ? raw : [raw]
      // Icons may still be available when the host lacks a label for a package.
      for (const name of batch) {
        for (const original of requested.get(name) ?? [])
          result.set(original, { label: original, icon: "ksu://icon/" + name })
      }
      for (const info of infos) {
        if (!info || typeof info !== "object" || "error" in info) continue
        const entry = info as Record<string, unknown>
        if (typeof entry.packageName !== "string" || !batch.includes(entry.packageName)) continue
        const label = typeof entry.appLabel === "string" && entry.appLabel.trim() ? entry.appLabel.trim() : entry.packageName
        for (const original of requested.get(entry.packageName) ?? [])
          result.set(original, { label, icon: "ksu://icon/" + entry.packageName })
      }
    } catch {
      break
    }
  }
  return result
}
