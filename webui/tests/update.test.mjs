import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { test } from "node:test"
import { setImmediate } from "node:timers/promises"
import { runInNewContext } from "node:vm"
import ts from "typescript"

const app = readFileSync(new URL("../src/App.svelte", import.meta.url), "utf8")
const i18n = readFileSync(new URL("../src/lib/i18n.ts", import.meta.url), "utf8")
const log = readFileSync(new URL("../src/lib/log.ts", import.meta.url), "utf8")
const compile = (source) => ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.CommonJS },
}).outputText

// Execute the component's actual handlers; bypass module discovery and DOM rendering.
function harness(t) {
  t.mock.timers.enable({ apis: ["setTimeout"] })
  const calls = []
  const toasts = []
  const timers = new Set()
  let mount
  const media = { matches: false, addEventListener() {}, removeEventListener() {} }
  const module = { exports: {} }
  const locale = { exports: {} }
  const logModule = { exports: {} }
  runInNewContext(compile(log), { exports: logModule.exports })
  runInNewContext(compile(i18n), { exports: locale.exports, navigator: { language: "en" } })
  const source = app.match(/<script lang="ts">([\s\S]*?)<\/script>/)[1]
    .replace('let moddir = $state("")', 'let moddir = $state("/data/adb/modules/keybox-lite")')
  const hooks = `
    export const handlers = {
      update, stopUpdate, appendOutput,
      mount: () => onMountForTest(),
      state: () => ({ busy, cancelling, cancelRequested, updateRunning, output, error })
    }
  `
  runInNewContext(compile(source + hooks), {
    exports: module.exports,
    require(name) {
      if (name === "svelte") return { onMount: (fn) => { mount = fn } }
      if (name === "./lib/i18n") return locale.exports
      if (name === "./lib/log") return logModule.exports
      if (name === "./lib/bridge") return {
        exec(command, onLine) {
          if (command.includes("action.sh") || command.includes("cancel-update")) {
            return new Promise((resolve) => calls.push({ command, onLine, resolve }))
          }
          if (command.endsWith(' info')) return Promise.resolve({
            errno: 0, stdout: JSON.stringify({ path: "", exists: false, size: 0, modified: 0, structure_valid: false, serials: [] }), stderr: ""
          })
          return Promise.resolve({ errno: 0, stdout: command.includes("settings.sh") ? "SCHEDULE=off\nLAST_RUN=0" : "", stderr: "" })
        },
        toast: (...args) => toasts.push(args),
        enableEdgeToEdge() {},
      }
      return {}
    },
    $state: Object.assign((value) => value, { raw: (value) => value }),
    $derived: (value) => value,
    $effect() {},
    localStorage: { getItem() { return null } },
    window: { matchMedia: () => media },
    onMountForTest: () => mount(),
    setTimeout(fn, ms) {
      const id = setTimeout(() => { timers.delete(id); fn() }, ms)
      timers.add(id)
      return id
    },
    clearTimeout(id) { timers.delete(id); clearTimeout(id) },
  })
  return { ...module.exports.handlers, calls, toasts, timers }
}
const result = (errno, extra = {}) => ({ errno, stdout: "", stderr: "", ...extra })

test("stop is unavailable when idle and deduplicated while pending and after acceptance", async (t) => {
  const h = harness(t)
  h.stopUpdate()
  assert.equal(h.calls.length, 0)
  const update = h.update()
  const stop = h.stopUpdate()
  h.stopUpdate()
  assert.equal(h.calls.length, 2)
  assert.equal(h.state().cancelling, true)
  h.calls[1].resolve(result(0))
  await stop
  assert.equal(h.state().cancelRequested, true)
  assert.equal(h.state().cancelling, false)
  h.stopUpdate()
  assert.equal(h.calls.length, 2)
  h.calls[0].resolve(result(130))
  await update
  assert.equal(h.toasts[0][0], "Update stopped")
  assert.equal(h.state().busy, null)
  assert.equal(h.state().cancelRequested, false)
  assert.equal(h.state().updateRunning, false)
  h.stopUpdate()
  assert.equal(h.calls.length, 2)
})

test("update completion waits for a late stop response before classifying cancellation", async (t) => {
  const h = harness(t)
  const update = h.update()
  const stop = h.stopUpdate()
  h.calls[0].resolve(result(130))
  await setImmediate()
  assert.equal(h.state().updateRunning, false)
  assert.equal(h.toasts.length, 0)
  h.stopUpdate()
  assert.equal(h.calls.length, 2)
  h.calls[1].resolve(result(0))
  await stop
  await update
  assert.equal(h.toasts[0][0], "Update stopped")
})

test("failed stop allows retry; normal success wins a race with stopping", async (t) => {
  const h = harness(t)
  const update = h.update()
  const first = h.stopUpdate()
  h.calls[1].resolve(result(1, { stderr: "denied" }))
  await first
  assert.equal(h.state().cancelRequested, false)
  assert.equal(h.state().error, "denied")
  const retry = h.stopUpdate()
  assert.equal(h.calls.length, 3)
  h.calls[2].resolve(result(0))
  await retry
  h.calls[0].resolve(result(0))
  await update
  assert.equal(h.toasts[0][0], "Keybox updated")
})

test("accepted stop does not disguise bridge timeout or unrelated failure", async (t) => {
  for (const r of [result(-1, { failure: "timeout" }), result(1)]) {
    const h = harness(t)
    const update = h.update()
    const stop = h.stopUpdate()
    h.calls[1].resolve(result(0))
    await stop
    h.calls[0].resolve(r)
    await update
    assert.equal(h.toasts[0][0], "Update failed")
    assert.ok(h.state().error)
    t.mock.timers.reset()
  }
})

test("logs batch in order, stay bounded, flush on exit, and release timer on unmount", async (t) => {
  const h = harness(t)
  const update = h.update()
  const cleanup = h.mount()
  for (let i = 0; i < 100; i++) h.calls[0].onLine(String(i))
  assert.equal(h.timers.size, 1)
  assert.equal(h.state().output, "")
  t.mock.timers.tick(50)
  assert.equal(h.state().output, Array.from({ length: 100 }, (_, i) => i + "\n").join(""))
  h.calls[0].onLine("\x1b[33m" + "x".repeat(90_000) + "\x1b[0m")
  h.calls[0].resolve(result(0))
  await update
  assert.equal(h.state().output.length, 80_000)
  assert.equal(h.state().output, "x".repeat(79_999) + "\n")
  assert.equal(h.timers.size, 0)
  h.appendOutput("pending")
  cleanup()
  assert.equal(h.timers.size, 0)
  const previous = h.state().output
  h.appendOutput("late")
  t.mock.timers.tick(50)
  assert.equal(h.state().output, previous)
})
