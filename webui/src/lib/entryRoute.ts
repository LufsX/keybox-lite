import { moduleInfo } from "kernelsu"

const injectedModules = new Set(["tricky_store", "oh_my_keymint", "teesim"])

export function initializeEntryRoute() {
  if (window.location.hash && window.location.hash !== "#" && window.location.hash !== "#/") return
  try {
    const info: unknown = JSON.parse(moduleInfo())
    if (!info || typeof info !== "object") return
    const value = info as { id?: unknown; moduleDir?: unknown }
    const id = typeof value.id === "string" && value.id ? value.id :
      typeof value.moduleDir === "string" ? value.moduleDir.replace(/\/+$/, "").split("/").pop() : ""
    if (!id || !injectedModules.has(id)) return
    const url = new URL(window.location.href)
    url.hash = "/targets"
    window.history.replaceState(window.history.state, "", url)
  } catch {
    // Hosts without module metadata retain their explicit route.
  }
}
