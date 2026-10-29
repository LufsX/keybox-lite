const marker = "__keyboxLiteModal"

interface Entry {
  id: string
  url: string
  state: Record<string, unknown>
  close: () => boolean
  armed: boolean
  consumed: boolean
}

const entries: Entry[] = []
const instance = Date.now().toString(36) + "-" + Math.random().toString(36).slice(2, 9)
let sequence = 0
let returning = false
let finishReturn: (() => void) | undefined
let listening = false

function arm(entry: Entry) {
  try {
    window.history.pushState({ ...entry.state, [marker]: entry.id }, "", entry.url)
    entry.armed = true
  } catch {
    // Hosts that disable session history retain the dialog's native controls.
  }
}

function detachIfIdle() {
  if (entries.length || returning || !listening) return
  window.removeEventListener("popstate", modalBack)
  listening = false
}

function modalBack() {
  if (returning) {
    returning = false
    // A new dialog may mount before the previous Back traversal completes.
    for (const entry of entries) if (!entry.armed && !entry.consumed) arm(entry)
    const finish = finishReturn
    finishReturn = undefined
    finish?.()
    detachIfIdle()
    return
  }
  let entry: Entry | undefined
  for (let index = entries.length - 1; index >= 0; index--) {
    if (entries[index].armed && !entries[index].consumed) { entry = entries[index]; break }
  }
  if (!entry || window.history.state?.[marker] === entry.id) return
  entry.armed = false
  entry.consumed = entry.close()
  if (!entry.consumed) arm(entry)
}

/** Return false from close while the dialog must stay open. */
export function registerModalBack(close: () => boolean): () => Promise<void> {
  const state = window.history.state
  const entry: Entry = {
    id: instance + "-" + ++sequence,
    url: window.location.href,
    state: state && typeof state === "object" ? state : {},
    close,
    armed: false,
    consumed: false,
  }
  entries.push(entry)
  if (!listening) {
    window.addEventListener("popstate", modalBack)
    listening = true
  }
  if (!returning) arm(entry)

  let released = false
  let settled = Promise.resolve()
  return () => {
    if (released) return settled
    released = true
    const index = entries.indexOf(entry)
    if (index >= 0) entries.splice(index, 1)
    if (entry.armed && window.history.state?.[marker] === entry.id && window.location.href === entry.url) {
      // Removing the dialog's own entry keeps the next Back on the page route.
      returning = true
      settled = new Promise<void>(resolve => { finishReturn = resolve })
      window.history.back()
    }
    detachIfIdle()
    return settled
  }
}
