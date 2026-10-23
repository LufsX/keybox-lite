import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { test } from "node:test"
import { runInNewContext } from "node:vm"
import { compile as compileSvelte } from "svelte/compiler"
import { render } from "svelte/server"
import ts from "typescript"

const app = readFileSync(new URL("../src/App.svelte", import.meta.url), "utf8")
const transpile = (source, module = ts.ModuleKind.CommonJS) => ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2020, module },
}).outputText
const modules = new Map()
for (const name of ["i18n", "log", "certificateStatus", "ui"]) {
  const exports = {}
  runInNewContext(transpile(readFileSync(new URL("../src/lib/" + name + ".ts", import.meta.url), "utf8")), { exports })
  modules.set("./lib/" + name, exports)
}

function harness(hash = "") {
  const exports = {}, listeners = new Map(), removed = []
  let mount
  const entries = [hash]
  let cursor = 0
  const location = {
    get hash() { return hash },
    set hash(value) {
      const next = value && !value.startsWith("#") ? "#" + value : value
      if (next === hash) return
      hash = next
      entries.splice(cursor + 1)
      entries.push(hash)
      cursor++
    },
  }
  const media = { matches: false, addEventListener() {}, removeEventListener() {} }
  const source = app.match(/<script lang="ts">([\s\S]*?)<\/script>/)[1]
  runInNewContext(transpile(source + `
    export const handlers = {
      syncRoute, returnHome, setLanguage, setTheme,
      guard: value => { targetManager = { canLeave: value } },
      state: () => ({ targetsPage, lang, theme }),
    }
  `), {
    exports,
    require(name) {
      if (name === "svelte") return { onMount: fn => { mount = fn } }
      if (modules.has(name)) return modules.get(name)
      if (name === "./lib/bridge") return { exec: () => new Promise(() => {}), enableEdgeToEdge() {} }
      return {}
    },
    $state: Object.assign(value => value, { raw: value => value }),
    $derived: value => value,
    $effect() {},
    localStorage: { getItem() { return null }, setItem() {} },
    window: {
      location,
      matchMedia: () => media,
      addEventListener: (name, fn) => listeners.set(name, fn),
      removeEventListener(name, fn) { removed.push([name, fn]); listeners.delete(name) },
    },
    clearTimeout() {},
  })
  return {
    ...exports.handlers, location, listeners, removed,
    mount: () => mount(),
    hash(value) { location.hash = value; listeners.get("hashchange")() },
    back() { if (cursor > 0) { hash = entries[--cursor]; listeners.get("hashchange")() } },
    forward() { if (cursor + 1 < entries.length) { hash = entries[++cursor]; listeners.get("hashchange")() } },
  }
}

test("home entry opens targets; browser back/forward and direct hash entry preserve theme and language", () => {
  const h = harness()
  const cleanup = h.mount()
  assert.equal(h.state().targetsPage, false)
  h.setLanguage("zh")
  h.setTheme("dark")
  h.hash("#/targets")
  assert.equal(h.state().targetsPage, true)
  h.back()
  assert.equal(h.state().targetsPage, false)
  h.forward()
  assert.equal(h.state().targetsPage, true)
  assert.equal(h.state().lang, "zh")
  assert.equal(h.state().theme, "dark")
  const handler = h.listeners.get("hashchange")
  cleanup()
  assert.deepEqual(h.removed, [["hashchange", handler]])
  assert.equal(h.listeners.size, 0)
  const direct = harness("#/targets")
  assert.equal(direct.state().targetsPage, true)
  direct.mount()()
})

test("return link and browser hash navigation obey the same guard without duplicate confirmation", () => {
  const h = harness("#/targets")
  const cleanup = h.mount()
  let allowed = false, checks = 0, prevented = 0
  h.guard(() => { checks++; return allowed })
  h.returnHome({ preventDefault() { prevented++ } })
  assert.equal(h.location.hash, "#/targets")
  assert.equal(h.state().targetsPage, true)
  h.hash("#/")
  assert.equal(h.location.hash, "#/targets")
  assert.equal(h.state().targetsPage, true)
  h.hash("#/elsewhere")
  assert.equal(h.location.hash, "#/targets")
  assert.equal(checks, 3)
  allowed = true
  h.returnHome({ preventDefault() { prevented++ } })
  assert.equal(prevented, 2)
  assert.equal(h.location.hash, "#/")
  h.syncRoute()
  assert.equal(checks, 4)
  assert.equal(h.state().targetsPage, false)
  h.hash("#/targets")
  h.hash("#/")
  assert.equal(checks, 5)
  assert.equal(h.state().targetsPage, false)
  cleanup()
})

test("rejected browser Back keeps a home history entry usable after the guard clears", () => {
  const h = harness()
  const cleanup = h.mount()
  h.hash("#/targets")
  let allowed = false
  h.guard(() => allowed)
  for (let attempt = 0; attempt < 2; attempt++) {
    h.back()
    assert.equal(h.state().targetsPage, true)
    assert.equal(h.location.hash, "#/targets")
    h.syncRoute() // The hash event from restoring the target entry.
  }
  allowed = true
  h.back()
  assert.equal(h.state().targetsPage, false)
  assert.equal(h.location.hash, "")
  cleanup()
})

async function renderApp(hash) {
  let source = app.replace('typeof window !== "undefined" && window.location.hash === "#/targets"', String(hash === "#/targets"))
  const { js } = compileSvelte(source, { generate: "server" })
  const stub = 'data:text/javascript,' + encodeURIComponent('export default function Stub($$renderer) { $$renderer.push("<div data-target-manager></div>") }')
  const empty = 'data:text/javascript,' + encodeURIComponent('export default function Stub() {}')
  const icon = 'data:text/javascript,' + encodeURIComponent('export function ArrowLeft() {} export const ListChecks=ArrowLeft, EllipsisVertical=ArrowLeft, ChevronRight=ArrowLeft, RefreshCw=ArrowLeft, Loader2=ArrowLeft, ShieldCheck=ArrowLeft, ShieldAlert=ArrowLeft, ShieldQuestion=ArrowLeft, Languages=ArrowLeft, HardDrive=ArrowLeft, Download=ArrowLeft, CalendarClock=ArrowLeft, History=ArrowLeft, Monitor=ArrowLeft, Sun=ArrowLeft, Moon=ArrowLeft, Square=ArrowLeft')
  const code = js.code.replace(/from (['"])([^'"]+)\1/g, (_, quote, name) => {
    let url
    if (name.endsWith("TargetManager.svelte")) url = stub
    else if (name.endsWith(".svelte")) url = empty
    else if (name === "lucide-svelte") url = icon
    else if (name === "./lib/bridge") url = 'data:text/javascript,' + encodeURIComponent('export function exec() { throw Error("unexpected I/O") } export function toast() {} export function enableEdgeToEdge() {}')
    else if (modules.has(name)) url = 'data:text/javascript;base64,' + Buffer.from(transpile(readFileSync(new URL("../src/" + name.slice(2) + ".ts", import.meta.url), "utf8"), ts.ModuleKind.ESNext)).toString("base64")
    else url = import.meta.resolve(name)
    return "from " + quote + url + quote
  })
  const { default: App } = await import("data:text/javascript;base64," + Buffer.from(code).toString("base64"))
  return render(App).body
}

test("compiled App renders one home entry and a separate target page with return link and shared header", async () => {
  const home = await renderApp("")
  assert.equal((home.match(/href="#\/targets"/g) || []).length, 1)
  assert.ok(!home.includes("data-target-manager"))
  const targets = await renderApp("#/targets")
  assert.ok(targets.includes("data-target-manager"))
  assert.ok(targets.includes('href="#/"'))
  assert.ok(!targets.includes('href="#/targets"'))
  assert.ok(!targets.includes(modules.get("./lib/i18n").messages.en.installed))
  for (const body of [home, targets]) assert.ok(body.includes("<header"))
  assert.ok(home.includes("Keybox Lite"))
  assert.ok(home.includes('src="./icon.svg"'))
  assert.ok(!targets.includes("Keybox Lite"))
  assert.ok(!targets.includes('src="./icon.svg"'))
  assert.ok(targets.includes('aria-label="' + modules.get("./lib/i18n").messages.en.targetsBack + '"'))
  assert.ok(targets.includes('id="targets-menu-trigger"'))
  assert.ok(targets.includes('aria-haspopup="menu"'))
  assert.ok(targets.includes('aria-expanded="false"'))
  assert.equal((targets.match(/href="#\/"/g) || []).length, 1)
})
