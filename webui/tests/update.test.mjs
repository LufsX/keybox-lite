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
function harness(t, savedSource = null, savedSkip = "0") {
  t.mock.timers.enable({ apis: ["setTimeout"] })
  const calls = []
  const toasts = []
  const timers = new Set()
  let storedSource = savedSource ?? "auto"
  let storedSkip = savedSkip
  let storedSchedule = "off"
  let settingsFailure = false
  let mount
  const media = { matches: false, addEventListener() {}, removeEventListener() {} }
  const module = { exports: {} }
  const locale = { exports: {} }
  const logModule = { exports: {} }
  runInNewContext(compile(log), { exports: logModule.exports })
  runInNewContext(compile(i18n), { exports: locale.exports, navigator: { language: "en" } })
  const source = app.match(/<script lang="ts">([\s\S]*?)<\/script>/)[1]
    .replace('let moddir = $state("")', 'let moddir = $state("/data/adb/modules/keybox-lite")')
  const targetDisabled = app.match(/<TargetManager\b[^>]*disabled=\{([^}]+)\}/)[1]
  const hooks = `
    export const handlers = {
      update, stopUpdate, appendOutput, setSource, setSkipRevocation, loadMeta, setSchedule,
      scheduleState: () => ({ saved: schedule, pending: scheduleChoice }),
      skipsRevocation: () => skipRevocation,
      openTargets: () => { window.location.hash = "#/targets"; syncRoute() },
      targetsDisabled: () => (${targetDisabled}),
      targetsPage: () => targetsPage,
      source: () => keyboxSource,
      mount: () => onMountForTest(),
      state: () => ({ busy, cancelling, cancelRequested, updateRunning, output, error })
    }
  `
  runInNewContext(compile(source + hooks), {
    exports: module.exports,
    AbortController,
    require(name) {
      if (name === "svelte") return { onMount: (fn) => { mount = fn }, tick: () => Promise.resolve() }
      if (name === "./lib/i18n") return locale.exports
      if (name === "./lib/log") return logModule.exports
      if (name === "./lib/bridge") return {
        execUpdate(command, onOutput, timeout, signal, onStarted) {
          onStarted(() => new Promise((resolve) => calls.push({ command: "stop-owned-supervisor", resolve })))
          return new Promise((resolve) => calls.push({ command, onLine: (line) => module.exports.handlers.appendOutput(line), onOutput, signal, resolve }))
        },
        exec(command, onLine) {
          if (command.includes("action.sh") || command.includes("cancel-update")) {
            return new Promise((resolve) => calls.push({ command, onLine, resolve }))
          }
          if (command.endsWith(' info')) return Promise.resolve({
            errno: 0, stdout: JSON.stringify({ path: "", exists: false, size: 0, modified: 0, structure_valid: false, serials: [] }), stderr: ""
          })
          if (command.includes("settings.sh")) {
            if (settingsFailure) return Promise.resolve({ errno: 1, stdout: "", stderr: "save failed" })
            if (command.includes('settings.sh" set ')) storedSchedule = command.trim().split(/\s+/).pop()
            if (command.includes("source ")) storedSource = command.trim().split(/\s+/).pop()
            if (command.includes("skip-revocation ")) storedSkip = command.trim().split(/\s+/).pop()
            return Promise.resolve({ errno: 0, stdout: "SCHEDULE=" + storedSchedule + "\nLAST_RUN=0\nSOURCE=" + storedSource + (storedSkip === null ? "" : "\nSKIP_REVOCATION=" + storedSkip), stderr: "" })
          }
          return Promise.resolve({ errno: 0, stdout: "", stderr: "" })
        },
        toast: (...args) => toasts.push(args),
        enableEdgeToEdge() {},
      }
      return {}
    },
    $state: Object.assign((value) => value, { raw: (value) => value }),
    $derived: (value) => value,
    $effect() {},
    localStorage: { getItem(key) { return key === "keybox-lite-source" ? savedSource : null }, setItem() {} },
    window: { matchMedia: () => media, location: { hash: "" }, scrollTo() {}, addEventListener() {}, removeEventListener() {} },
    onMountForTest: () => mount(),
    setTimeout(fn, ms) {
      const id = setTimeout(() => { timers.delete(id); fn() }, ms)
      timers.add(id)
      return id
    },
    clearTimeout(id) { timers.delete(id); clearTimeout(id) },
  })
  return { ...module.exports.handlers, calls, toasts, timers, failSettings: value => { settingsFailure = value } }
}
const result = (errno, extra = {}) => ({ errno, stdout: "", stderr: "", ...extra })

test("schedule moves immediately, blocks duplicate changes, and returns to the saved value on failure", async (t) => {
  const h = harness(t)
  await h.loadMeta()
  const saving = h.setSchedule("weekly")
  assert.equal(h.scheduleState().pending, "weekly")
  await h.setSchedule("daily")
  assert.equal(h.scheduleState().pending, "weekly")
  await saving
  assert.equal(h.scheduleState().saved, "weekly")
  assert.equal(h.scheduleState().pending, null)
  h.failSettings(true)
  const failed = h.setSchedule("daily")
  assert.equal(h.scheduleState().pending, "daily")
  await failed
  assert.equal(h.scheduleState().saved, "weekly")
  assert.equal(h.scheduleState().pending, null)
  assert.match(h.state().error, /save failed/)
})

test("WebUI source selection persists and every update uses the saved source", async (t) => {
  const h = harness(t)
  for (const source of ["auto", "meow", "spectre"]) {
    await h.setSource(source)
    assert.equal(h.source(), source)
    const update = h.update()
    const call = h.calls.at(-1)
    assert.equal(call.command, `"/data/adb/modules/keybox-lite/bin/keyboxlite" run-update "/data/adb/modules/keybox-lite/action.sh" /data/adb/keybox-lite`)
    h.setSource(source === "meow" ? "spectre" : "meow")
    assert.equal(h.source(), source, "selection cannot change during an update")
    call.resolve(result(0))
    await update
  }
  h.setSource("spectre'; touch /tmp/untrusted")
  await Promise.resolve()
  assert.equal(h.source(), "spectre")
})

test("skip revocation persists, rejects malformed settings, and stays unchanged on failure or busy updates", async (t) => {
  const h = harness(t)
  await h.loadMeta()
  assert.equal(h.skipsRevocation(), false)
  await h.setSkipRevocation(true)
  assert.equal(h.skipsRevocation(), true)
  h.failSettings(true)
  await h.setSkipRevocation(false)
  assert.equal(h.skipsRevocation(), true)
  assert.match(h.state().error, /save failed/)
  h.failSettings(false)
  const pending = h.update()
  await h.setSkipRevocation(false)
  assert.equal(h.skipsRevocation(), true)
  h.calls[0].resolve(result(0))
  await pending
  await h.setSkipRevocation(false)
  assert.equal(h.skipsRevocation(), false)
  t.mock.timers.reset()
  const missing = harness(t, null, null)
  await missing.loadMeta()
  assert.equal(missing.skipsRevocation(), false)
  t.mock.timers.reset()
  await assert.rejects(() => harness(t, null, "yes").loadMeta())
})

test("Tricky Store remains available while an update is pending", async (t) => {
  const h = harness(t)
  const pending = h.update()
  assert.equal(h.state().busy, "update")
  h.openTargets()
  assert.equal(h.targetsPage(), true)
  assert.equal(h.targetsDisabled(), false)
  assert.equal(h.calls.length, 1)
  h.calls[0].resolve(result(0))
  await pending
  assert.equal(h.state().busy, null)
  assert.equal(h.targetsDisabled(), false)
})

test("settings source loads supported providers and rejects an unsupported value", async (t) => {
  for (const [stored, expected] of [
    [null, "auto"], ["auto", "auto"], ["meow", "meow"], ["spectre", "spectre"],
  ]) {
    const h = harness(t, stored)
    await h.loadMeta()
    assert.equal(h.source(), expected)
    t.mock.timers.reset()
  }
  await assert.rejects(() => harness(t, "addon").loadMeta())
})

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

test("update snapshots replace logs and destruction aborts monitoring without a late toast", async (t) => {
  const h = harness(t)
  const pending = h.update()
  const call = h.calls[0]
  call.onOutput("first\n")
  call.onOutput("first\nsecond\n")
  assert.equal(h.state().output, "first\nsecond\n")
  const cleanup = h.mount()
  cleanup()
  assert.equal(call.signal.aborted, true)
  call.onOutput("late")
  call.resolve(result(0))
  await pending
  assert.equal(h.state().output, "first\nsecond\n")
  assert.equal(h.toasts.length, 0)
  assert.equal(h.timers.size, 0)
})

test("update failure preserves earlier stop diagnostics", async (t) => {
  const h = harness(t)
  const pending = h.update()
  const stop = h.stopUpdate()
  h.calls[1].resolve(result(1, { stderr: "stop denied" }))
  await stop
  h.calls[0].resolve(result(1, { stderr: "download failed" }))
  await pending
  assert.equal(h.state().error, "stop denied\ndownload failed")
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
