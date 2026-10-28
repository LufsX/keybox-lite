import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { test } from "node:test"
import { runInNewContext } from "node:vm"
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

function harness(hash = "", respond) {
  const exports = {}, listeners = new Map(), removed = [], calls = []
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
      if (name === "svelte") return { onMount: fn => { mount = fn }, tick: () => Promise.resolve() }
      if (name === "./lib/motion") return { pageExit: () => ({ duration: 0, css: () => "" }) }
      if (modules.has(name)) return modules.get(name)
      if (name === "./lib/bridge") return { exec: command => { calls.push(command); return respond ? Promise.resolve(respond(command)) : new Promise(() => {}) }, enableEdgeToEdge() {} }
      return {}
    },
    $state: Object.assign(value => value, { raw: value => value }),
    $derived: value => value,
    $effect() {},
    localStorage: { getItem() { return null }, setItem() {} },
    window: {
      location,
      scrollTo() {},
      matchMedia: () => media,
      addEventListener: (name, fn) => listeners.set(name, fn),
      removeEventListener(name, fn) { removed.push([name, fn]); listeners.delete(name) },
    },
    clearTimeout() {},
  })
  return {
    ...exports.handlers, location, listeners, removed, calls,
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

test("direct targets startup skips home reads and loads home once on return", async () => {
  const h = harness("#/targets", command => {
    let stdout = ""
    if (command.includes("for d in")) stdout = "/data/adb/modules/keybox-lite"
    else if (command.endsWith(" info")) stdout = JSON.stringify({ path: "", exists: false, size: 0, modified: 0, structure_valid: false, serials: [] })
    else if (command.includes("settings.sh")) stdout = "SCHEDULE=off\nLAST_RUN=0"
    else if (command.includes("module.prop")) stdout = "version=2.0.0\n---\n"
    return { errno: 0, stdout, stderr: "" }
  })
  const cleanup = h.mount()
  const settle = async () => { for (let i = 0; i < 30; i++) await Promise.resolve() }
  await settle()
  assert.equal(h.calls.length, 1)
  assert.match(h.calls[0], /for d in/)
  h.returnHome({ preventDefault() {} })
  await settle()
  assert.equal(h.calls.filter(command => command.endsWith(" info")).length, 1)
  assert.equal(h.calls.filter(command => command.includes("settings.sh")).length, 1)
  assert.equal(h.calls.filter(command => command.includes("module.prop")).length, 1)
  h.hash("#/targets")
  h.hash("#/")
  await settle()
  assert.equal(h.calls.length, 4, "returning again does not reload initialized home")
  cleanup()
})
