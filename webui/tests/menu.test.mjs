import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { test } from "node:test"
import { runInNewContext } from "node:vm"
import { spawnSync } from "node:child_process"
import { setImmediate } from "node:timers/promises"
import ts from "typescript"

const app = readFileSync(new URL("../src/App.svelte", import.meta.url), "utf8")
const compile = source => ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.CommonJS },
}).outputText
function loadModule(path, globals = {}) {
  const module = { exports: {} }
  runInNewContext(compile(readFileSync(new URL(path, import.meta.url), "utf8")), { exports: module.exports, ...globals })
  return module.exports
}
const locale = loadModule("../src/lib/i18n.ts", { navigator: { language: "en" } })
const logs = loadModule("../src/lib/log.ts")
const certificateStatus = loadModule("../src/lib/certificateStatus.ts")
const targets = loadModule("../src/lib/targets.ts", { require: () => ({}) })
const dropdown = readFileSync(new URL("../src/lib/components/Dropdown.svelte", import.meta.url), "utf8")

function harness({ saved, storageFails = false } = {}) {
  const writes = []
  const calls = []
  let nextResult = { errno: 0, stdout: "[DONE] saved update\n", stderr: "" }
  let nextInfo = { path: "/keybox.xml", exists: true, size: 10, modified: 1, structure_valid: true, serials: ["1"] }
  const infoReads = []
  const module = { exports: {} }
  const source = app.match(/<script lang="ts">([\s\S]*?)<\/script>/)[1]
    .replace('let moddir = $state("")', 'let moddir = $state("/data/adb/modules/keybox-lite")')
  const hooks = `
    export const handlers = {
      toggleTheme, toggleLanguage, setTheme, setLanguage,
      targetLinkTitle: () => targetBackendTitle(info?.backend, messages[lang].targetsTitle),
      showLog, showCurrentOutput, checkRevocation, update, loadInfo, shellQuote,
      seed: () => { setOutput("current output"); checkRc = 1 },
      setBusy: value => { busy = value },
      setError: value => { error = value },
      state: () => ({ lang, theme, themeOpen, languageOpen, savedLogVisible, output, savedOutput, savedOutputLines, logLoading, checkRc, certificateVerification, info, infoStale, error, busy })
    }
  `
  runInNewContext(compile(source + hooks), {
    exports: module.exports,
    AbortController,
    require(name) {
      if (name === "svelte") return { onMount() {}, tick: () => Promise.resolve() }
      if (name === "./lib/i18n") return locale
      if (name === "./lib/log") return logs
      if (name === "./lib/certificateStatus") return certificateStatus
      if (name === "./lib/targets") return targets
      if (name === "./lib/bridge") return {
        exec(command) {
          calls.push(command)
          if (command.endsWith(" info")) {
            if (infoReads.length) return Promise.resolve(infoReads.shift())
            return Promise.resolve({ errno: 0, stdout: JSON.stringify(nextInfo), stderr: "" })
          }
          if (command.includes("settings.sh")) return Promise.resolve({ errno: 0, stdout: "SCHEDULE=off\nLAST_RUN=0", stderr: "" })
          return Promise.resolve(nextResult)
        },
        toast() {}, enableEdgeToEdge() {},
      }
      return {}
    },
    $state: Object.assign(value => value, { raw: value => value }),
    $derived: value => value,
    $effect() {},
    localStorage: {
      getItem() { if (storageFails) throw new Error("disabled"); return saved ?? null },
      setItem(key, value) { if (storageFails) throw new Error("disabled"); writes.push([key, value]) },
    },
    clearTimeout() {},
  })
  const handlers = module.exports.handlers
  return { ...handlers, writes, calls, result: value => { nextResult = value }, info: value => { nextInfo = value }, queueInfo: (...values) => infoReads.push(...values) }
}

test("home target entry uses validated detected metadata and keeps its route across backend changes", async () => {
  const h = harness()
  const base = { path: "/keybox.xml", exists: true, size: 10, modified: 1, structure_valid: true, serials: ["1"] }
  assert.equal(h.targetLinkTitle(), locale.messages.en.targetsTitle)
  for (const [identity, expected] of [["TS-L", "Tricky Store"], ["TEES-RS", "TEESimulator-RS"], ["TEES", "TEESimulator"], ["OMK", "OhMyKeymint"]]) {
    h.info({ ...base, backend: { identity, active: true, version: "1.0" } })
    await h.loadInfo()
    assert.equal(h.targetLinkTitle(), expected)
  }
  h.info({ ...base, backend: { identity: "TEES-RS", active: true, name: "Installed fork", version: "2.0" } })
  await h.loadInfo()
  assert.equal(h.targetLinkTitle(), "Installed fork")
  for (const backend of [{ identity: "TS", active: true, name: 42 }, { identity: "TS", active: true, version: {} }]) {
    h.info({ ...base, backend })
    await assert.rejects(h.loadInfo(), /Invalid keybox information/)
    assert.equal(h.state().infoStale, true)
    assert.equal(h.targetLinkTitle(), "Installed fork")
  }
  h.info({ ...base, backend: null })
  await h.loadInfo()
  h.setLanguage("zh")
  assert.equal(h.targetLinkTitle(), locale.messages.zh.targetsTitle)
})

function dropdownHarness({ disabled = false } = {}) {
  const module = { exports: {} }
  const changes = []
  const choices = []
  let focused = 0
  class Node {}
  const inside = new Node()
  const outside = new Node()
  const source = dropdown.match(/<script[^>]*>([\s\S]*?)<\/script>/)[1]
  runInNewContext(compile(source + `
    export const handlers = {
      dismiss, keydown, select, navigate,
      setOpen: value => { open = value },
      bind: (element, trigger) => { control = element; menu = element; button = trigger }
    }
  `), {
    exports: module.exports,
    require(name) {
      if (name === "../ui") return loadModule("../src/lib/ui.ts")
      if (name === "svelte") return { onMount() {}, tick: () => Promise.resolve() }
      return {}
    },
    Node,
    requestAnimationFrame: callback => callback(),
    $state: value => value,
    $effect() {},
    document: { activeElement: null },
    $props: () => ({
      open: true, disabled, options: [{ value: "en" }, { value: "ja" }, { value: "zh" }], selected: "ja",
      onOpenChange(value) { changes.push(value); module.exports.handlers.setOpen(value) },
      onSelect(value) { choices.push(value) },
    }),
  })
  module.exports.handlers.bind({ contains: node => node === inside }, { focus: () => { focused++ } })
  return { ...module.exports.handlers, changes, choices, inside, outside, focused: () => focused }
}

test("language and theme menus toggle mutually exclusively", () => {
  const h = harness()
  h.toggleLanguage()
  assert.equal(h.state().languageOpen, true)
  h.toggleTheme()
  assert.equal(h.state().languageOpen, false)
  assert.equal(h.state().themeOpen, true)
  h.toggleLanguage()
  assert.equal(h.state().themeOpen, false)
  h.toggleLanguage()
  assert.equal(h.state().languageOpen, false)
  h.toggleTheme(false)
  assert.equal(h.state().themeOpen, false)
})

test("shared Dropdown preserves inside clicks, closes outside and restores focus on Escape", () => {
  const h = dropdownHarness()
  h.dismiss({ target: h.inside })
  assert.deepEqual(h.changes, [])
  h.keydown({ key: "Enter" })
  assert.deepEqual(h.changes, [])
  h.dismiss({ target: h.outside })
  assert.deepEqual(h.changes, [false])
  h.setOpen(true)
  h.keydown({ key: "Escape", preventDefault() {}, stopImmediatePropagation() {} })
  assert.deepEqual(h.changes, [false, false])
  assert.equal(h.focused(), 1)
  h.keydown({ key: "Escape", preventDefault() {}, stopImmediatePropagation() {} })
  assert.equal(h.focused(), 1)
  assert.match(dropdown, /onpointerdown=\{dismiss\} onfocusin=\{dismiss\} onkeydowncapture=\{keydown\}/)
})

test("shared Dropdown chooses the option, closes and restores focus", () => {
  const h = dropdownHarness()
  h.select("ja")
  assert.deepEqual(h.choices, ["ja"])
  assert.deepEqual(h.changes, [false])
  assert.equal(h.focused(), 1)
})

test("shared Dropdown blocks disabled selection and keyboard opening", async () => {
  const h = dropdownHarness({ disabled: true })
  h.select("ja")
  await h.navigate({ key: "ArrowDown", preventDefault() { assert.fail("disabled trigger handled a key") } })
  assert.deepEqual(h.choices, [])
  assert.deepEqual(h.changes, [])
})

test("shared Dropdown opens at the selected option and supports endpoint keys", async () => {
  const h = dropdownHarness()
  const focus = []
  const items = ["en", "ja", "zh"].map(value => ({ focus: () => focus.push(value) }))
  h.bind({ querySelectorAll: () => items }, { focus() {} })
  h.setOpen(false)
  const key = key => h.navigate({ key, preventDefault() {} })
  await key("ArrowDown")
  await key("End")
  await key("Home")
  assert.deepEqual(h.changes, [true])
  assert.deepEqual(focus, ["ja", "zh", "en"])
})

test("selecting every supported language persists it and closes the menu", () => {
  for (const { code } of locale.supportedLanguages) {
    const h = harness({ saved: code })
    assert.equal(h.state().lang, code)
    h.toggleLanguage()
    h.setLanguage(code)
    assert.equal(h.state().lang, code)
    assert.equal(h.state().languageOpen, false)
    assert.deepEqual(h.writes, [["keybox-lite-language", code]])
  }
  const h = harness()
  h.toggleTheme()
  h.setTheme("dark")
  assert.equal(h.state().themeOpen, false)
  assert.deepEqual(h.writes, [["keybox-lite-theme", "dark"]])
})

test("invalid or unavailable storage falls back and still allows in-session choice", () => {
  assert.equal(harness({ saved: "invalid" }).state().lang, "en")
  const h = harness({ storageFails: true })
  assert.equal(h.state().lang, "en")
  h.toggleLanguage()
  h.setLanguage("ja")
  assert.equal(h.state().lang, "ja")
  assert.equal(h.state().languageOpen, false)
})

test("saved log entry reads last 80 persisted update lines, preserves the check result, and marks the title", async () => {
  const h = harness()
  h.seed()
  await h.showLog()
  assert.equal(h.calls.length, 1)
  assert.ok(h.calls[0].includes("tail -n 80 /data/local/tmp/keybox-update.log"))
  assert.equal(h.state().output, "current output")
  assert.equal(h.state().savedOutput, "[DONE] saved update")
  assert.equal(h.state().savedLogVisible, true)
  assert.equal(h.state().checkRc, 1)
  h.showCurrentOutput()
  assert.equal(h.state().savedLogVisible, false)
  assert.equal(h.state().output, "current output")
  assert.equal(h.state().savedOutput, "[DONE] saved update")
  await h.checkRevocation()
  assert.equal(h.state().savedLogVisible, false)
  await h.showLog()
  await h.update()
  assert.equal(h.state().savedLogVisible, false)
})

test("refreshing unchanged keybox data keeps the check; path, serial or certificate changes clear it", async () => {
  const h = harness()
  const base = {
    path: "/keybox.xml", exists: true, size: 10, modified: 1, structure_valid: true, serials: ["000123"],
    validity: { status: "valid", certificates: [{ serial: "000123", not_before: 1, not_after: 2, status: "valid" }] },
  }
  h.info(base)
  await h.loadInfo()
  h.seed()
  await h.loadInfo()
  assert.equal(h.state().checkRc, 1)
  for (const changed of [
    { ...base, path: "/other.xml" },
    { ...base, modified: 2 },
    { ...base, size: 11 },
    { ...base, serials: ["124"] },
    { ...base, validity: { status: "expired", certificates: [{ serial: "000123", not_before: 1, not_after: 2, status: "expired" }] } },
  ]) {
    h.info(base)
    await h.loadInfo()
    h.seed()
    h.info(changed)
    await h.loadInfo()
    assert.equal(h.state().checkRc, null)
  }
})

test("finished verification binds exact certificate results to the current keybox and ignores saved logs", async () => {
  const h = harness()
  const serial = "1234567890123456789012345678901234567890"
  h.info({ path: "/keybox.xml", exists: true, size: 10, modified: 1, structure_valid: true, serials: [serial, "2"] })
  await h.loadInfo()
  h.result({ errno: 1, stdout: "[CHECK] status: live (https://example.test)\n" +
    "[FAIL] REVOKED: serial 000" + serial + " -> REVOKED KEY_COMPROMISE\nRESULT: revoked\n", stderr: "" })
  await h.checkRevocation()
  const verification = h.state().certificateVerification
  assert.equal(verification.completed, true)
  assert.equal(certificateStatus.certificateStatusForSerial(verification, serial).status, "revoked")
  assert.equal(certificateStatus.certificateStatusForSerial(verification, serial).reason, "KEY_COMPROMISE")
  assert.equal(certificateStatus.certificateStatusForSerial(verification, "0002").status, "not-revoked")
  h.result({ errno: 0, stdout: "[CHECK] status: live (https://saved.test)\nRESULT: not revoked\n", stderr: "" })
  await h.showLog()
  assert.equal(h.state().certificateVerification, verification)
  assert.equal(h.state().checkRc, 1)
  await h.loadInfo()
  assert.equal(h.state().certificateVerification, verification)
  h.info({ path: "/different.xml", exists: true, size: 10, modified: 1, structure_valid: true, serials: [serial, "2"] })
  await h.loadInfo()
  assert.equal(h.state().certificateVerification, null)
  assert.equal(h.state().checkRc, null)
})

test("bridge failures cannot produce a passing certificate status and updates clear prior results", async () => {
  const h = harness()
  const stdout = "[CACHE] status: cache hit (<60s, https://example.test)\nRESULT: not revoked\n"
  h.result({ errno: 0, stdout, stderr: "" })
  await h.checkRevocation()
  assert.equal(h.state().certificateVerification.completed, true)
  h.result({ errno: -1, failure: "timeout", stdout, stderr: "" })
  await h.checkRevocation()
  assert.equal(h.state().certificateVerification.completed, false)
  assert.equal(certificateStatus.certificateStatusForSerial(h.state().certificateVerification, "1").status, "unknown")
  assert.equal(h.state().checkRc, 2)
  await h.update()
  assert.equal(h.state().certificateVerification, null)
})

test("a check finishing after the keybox identity changed cannot restore stale results", async () => {
  const h = harness()
  const base = { path: "/keybox.xml", exists: true, size: 10, modified: 1, structure_valid: true, serials: ["1"] }
  h.info(base)
  await h.loadInfo()
  let finish
  h.result(new Promise(resolve => { finish = resolve }))
  const check = h.checkRevocation()
  await setImmediate()
  assert.equal(h.calls.some(command => command.includes(" verify ")), true)
  h.info({ ...base, serials: ["2"] })
  await h.loadInfo()
  finish({ errno: 0, stdout: "[CHECK] status: live (https://example.test)\nRESULT: not revoked\n", stderr: "" })
  await check
  assert.equal(h.state().certificateVerification, null)
  assert.equal(h.state().checkRc, 2)
})

test("missing saved log is explicit; a failed read preserves current output and check result", async () => {
  const empty = harness()
  empty.result({ errno: 0, stdout: "", stderr: "" })
  await empty.showLog()
  assert.equal(empty.state().savedOutput, locale.messages.en.noLog)
  assert.equal(empty.state().output, "")
  assert.equal(empty.state().savedLogVisible, true)
  const failed = harness()
  failed.seed()
  failed.result({ errno: 1, stdout: "", stderr: "permission denied" })
  await failed.showLog()
  assert.equal(failed.state().output, "current output")
  assert.equal(failed.state().checkRc, 1)
  assert.equal(failed.state().savedLogVisible, true)
  assert.equal(failed.state().error, "permission denied")
  assert.equal(failed.state().busy, null)
})

const keyboxA = { path: "/keybox A.xml", exists: true, size: 10, modified: 1, structure_valid: true, serials: ["1"] }
const infoResult = info => ({ errno: 0, stdout: JSON.stringify(info), stderr: "" })
const cleanResult = { errno: 0, stdout: "[CHECK] status: live (https://example.test)\nRESULT: not revoked\n", stderr: "" }

test("a background replacement before check is freshly read and verified by its quoted snapshot path", async () => {
  const h = harness()
  h.info(keyboxA)
  await h.loadInfo()
  const replacement = { ...keyboxA, path: "/keybox B's $(printf nope) `printf nope`.xml", serials: ["2"], modified: 2 }
  h.info(replacement)
  h.result(cleanResult)
  await h.checkRevocation()
  assert.equal(h.state().info.path, replacement.path)
  assert.equal(h.state().checkRc, 0)
  assert.equal(h.calls.filter(command => command.endsWith(" info")).length, 3)
  assert.equal(h.calls.find(command => command.includes(" verify ")),
    '"/data/adb/modules/keybox-lite/bin/keyboxlite" verify ' + h.shellQuote(replacement.path))
  const quoted = spawnSync("sh", ["-c", "printf '%s' " + h.shellQuote(replacement.path)], { encoding: "utf8" })
  assert.equal(quoted.status, 0)
  assert.equal(quoted.stdout, replacement.path)
})

test("a background replacement during verify is detected by the post-check disk read", async () => {
  const h = harness()
  h.info(keyboxA)
  let finish
  h.result(new Promise(resolve => { finish = resolve }))
  const check = h.checkRevocation()
  await setImmediate()
  assert.equal(h.calls.some(command => command.includes(" verify ")), true)
  h.info({ ...keyboxA, serials: ["2"], modified: 2 })
  finish({ errno: 1, stdout: "[CHECK] status: live (https://example.test)\n[FAIL] REVOKED: serial 1 -> REVOKED compromised\nRESULT: revoked\n", stderr: "" })
  await check
  assert.equal(h.state().info.serials[0], "2")
  assert.equal(h.state().certificateVerification, null)
  assert.equal(h.state().checkRc, 2)
})

test("failed or invalid pre/post info reads never apply results; ambiguous targets are not verified", async () => {
  for (const failure of [
    { errno: 1, stdout: "", stderr: "read failed" },
    { errno: 0, stdout: "{broken", stderr: "" },
  ]) {
    for (const after of [false, true]) {
      const h = harness()
      h.info(keyboxA)
      if (after) h.queueInfo(infoResult(keyboxA), failure)
      else h.queueInfo(failure)
      h.result(cleanResult)
      await h.checkRevocation()
      assert.equal(h.state().checkRc, 2)
      assert.equal(h.state().certificateVerification, null)
      assert.equal(h.state().infoStale, true)
      assert.equal(h.calls.filter(command => command.includes(" verify ")).length, after ? 1 : 0)
      assert.ok(h.state().error)
    }
  }
  for (const snapshot of [
    { ...keyboxA, path: "" }, { ...keyboxA, path: "relative.xml" },
    { ...keyboxA, exists: false }, { ...keyboxA, structure_valid: false }, { ...keyboxA, serials: [] },
  ]) {
    const h = harness()
    h.info(snapshot)
    h.result(cleanResult)
    await h.checkRevocation()
    assert.equal(h.calls.some(command => command.includes(" verify ")), false)
    assert.equal(h.state().checkRc, 2)
  }
})

test("exit zero alone, empty output, conflicting summaries and mirror misses stay unavailable overall and individually", async () => {
  for (const stdout of [
    "", "RESULT: not revoked\n",
    cleanResult.stdout + "RESULT: revoked\n",
    "[CHECK] status: mirror (https://mirror.test)\nRESULT: not revoked\n",
    "[CHECK] status: live (https://example.test)\n[FAIL] status: invalid\nRESULT: not revoked\n",
  ]) {
    const h = harness()
    h.info(keyboxA)
    h.result({ errno: 0, stdout, stderr: "" })
    await h.checkRevocation()
    assert.equal(h.state().checkRc, 2)
    assert.equal(certificateStatus.certificateStatusForSerial(h.state().certificateVerification, "1").status, "unknown")
  }
})

test("live/cache completion can pass overall; matching mirror hits remain revoked", async () => {
  for (const route of [
    "[CHECK] status: live (https://example.test)",
    "[CACHE] status: cache hit (<60s, https://example.test)",
  ]) {
    const h = harness()
    h.info(keyboxA)
    h.result({ errno: 0, stdout: route + "\nRESULT: not revoked\n", stderr: "" })
    await h.checkRevocation()
    assert.equal(h.state().checkRc, 0)
    assert.equal(certificateStatus.certificateStatusForSerial(h.state().certificateVerification, "1").status, "not-revoked")
  }
  const h = harness()
  h.info(keyboxA)
  h.result({ errno: 1, stdout: "[CHECK] status: mirror (https://mirror.test)\n[FAIL] REVOKED: serial 1 -> SUSPENDED reason\nRESULT: revoked\n", stderr: "" })
  await h.checkRevocation()
  assert.equal(h.state().checkRc, 1)
  assert.equal(certificateStatus.certificateStatusForSerial(h.state().certificateVerification, "1").status, "suspended")
})

test("older info responses cannot overwrite a newer refresh or supply a check snapshot", async () => {
  const h = harness()
  let finishOld
  h.queueInfo(new Promise(resolve => { finishOld = resolve }))
  const oldRead = h.loadInfo()
  const newer = { ...keyboxA, serials: ["2"] }
  h.info(newer)
  await h.loadInfo()
  finishOld(infoResult(keyboxA))
  assert.equal(await oldRead, null)
  assert.equal(h.state().info.serials[0], "2")

  let finishPre
  h.queueInfo(new Promise(resolve => { finishPre = resolve }))
  const check = h.checkRevocation()
  await h.loadInfo()
  finishPre(infoResult(keyboxA))
  await check
  assert.equal(h.calls.some(command => command.includes(" verify ")), false)
  assert.equal(h.state().info.serials[0], "2")
  assert.equal(h.state().checkRc, 2)
})

test("a newer refresh superseding the post-check read prevents stale verification application", async () => {
  const h = harness()
  let finishPost
  h.queueInfo(infoResult(keyboxA), new Promise(resolve => { finishPost = resolve }))
  h.result(cleanResult)
  const check = h.checkRevocation()
  await setImmediate()
  h.info({ ...keyboxA, serials: ["2"] })
  await h.loadInfo()
  finishPost(infoResult(keyboxA))
  await check
  assert.equal(h.state().info.serials[0], "2")
  assert.equal(h.state().checkRc, 2)
  assert.equal(h.state().certificateVerification, null)
})


test("time-invalid output with rc3 retains its original hint only for a verified unchanged identity", async () => {
  for (const status of ["expired", "not_yet_valid"]) {
    const snapshot = { ...keyboxA, validity: { status, certificates: [{ serial: "1", not_before: 1, not_after: 2, status }] } }
    const stdout = "[CHECK] keybox: keybox A.xml\n[FAIL] validity: " + status +
      " (1 certificates)\n[FAIL] certificate 1: " + status + " (dates UTC)\nRESULT: " + status + " (device time)\n"
    const h = harness()
    h.info(snapshot)
    h.result({ errno: 3, stdout, stderr: "" })
    await h.checkRevocation()
    assert.equal(h.state().checkRc, 3)
    assert.equal(h.state().error, "")
    assert.equal(certificateStatus.certificateStatusForSerial(h.state().certificateVerification, "1").status, "unknown")
    for (const changed of [false, true]) {
      const rejected = harness()
      rejected.info(snapshot)
      if (changed) rejected.queueInfo(infoResult(snapshot), infoResult({ ...snapshot, serials: ["2"] }))
      rejected.result({ errno: 3, stdout: changed ? stdout : "RESULT: " + status + " (device time)\n", stderr: "" })
      await rejected.checkRevocation()
      assert.equal(rejected.state().checkRc, 2)
      assert.equal(certificateStatus.certificateStatusForSerial(rejected.state().certificateVerification, "1").status, "unknown")
    }
  }
})

test("switching output views shares a pending log read without replacing the selected view", async () => {
  const h = harness()
  h.seed()
  await h.showLog()
  h.showCurrentOutput()
  let finish
  h.result(new Promise(resolve => { finish = resolve }))
  const reading = h.showLog()
  assert.equal(h.state().logLoading, true)
  const count = h.calls.length
  await h.showLog()
  assert.equal(h.calls.length, count, "duplicate reads are blocked")
  h.showCurrentOutput()
  finish({ errno: 0, stdout: "late saved log", stderr: "" })
  await reading
  assert.equal(h.state().savedLogVisible, false)
  assert.equal(h.state().logLoading, false)
  assert.equal(h.state().output, "current output")
  assert.equal(h.state().savedOutput, "late saved log")
  h.result({ errno: 0, stdout: "new saved log", stderr: "" })
  await h.showLog()
  assert.equal(h.state().savedOutput, "new saved log")
  h.result({ errno: 1, stdout: "", stderr: "read failed" })
  await h.showLog()
  assert.equal(h.state().savedOutput, "new saved log", "failed refresh preserves the previous saved log")
  assert.equal(h.state().savedLogVisible, true)
  h.showCurrentOutput()
  assert.equal(h.state().output, "current output")
})

test("updates, checks and refreshes do not block log navigation or duplicate a pending read", async () => {
  for (const busy of ["update", "check", "refresh"]) {
    const h = harness()
    h.seed()
    await h.showLog()
    h.showCurrentOutput()
    h.setBusy(busy)
    h.setError("operation warning")
    let finish
    h.result(new Promise(resolve => { finish = resolve }))
    const reading = h.showLog()
    assert.equal(h.state().savedLogVisible, true, busy + ": cached logs select immediately")
    assert.equal(h.state().savedOutput, "[DONE] saved update")
    assert.equal(h.state().error, "operation warning", "view changes preserve operation errors")
    const reads = h.calls.length
    h.showCurrentOutput()
    assert.equal(h.state().savedLogVisible, false)
    assert.equal(h.state().logLoading, true, "navigation preserves the shared read")
    await h.showLog()
    assert.equal(h.state().savedLogVisible, true)
    assert.equal(h.calls.length, reads, "a loading view remains selectable without another command")
    h.showCurrentOutput()
    finish({ errno: 0, stdout: "saved during " + busy, stderr: "" })
    await reading
    assert.equal(h.state().savedLogVisible, false)
    assert.equal(h.state().savedOutput, "saved during " + busy)
    assert.equal(h.state().output, "current output")
    assert.equal(h.state().busy, busy)
    assert.equal(h.state().logLoading, false)
  }
})

test("starting verification retains its current view while an older log read fills the saved buffer", async () => {
  const h = harness()
  h.seed()
  let finish
  h.result(new Promise(resolve => { finish = resolve }))
  const reading = h.showLog()
  h.result({ errno: 0, stdout: "RESULT: not revoked", stderr: "" })
  await h.checkRevocation()
  const current = h.state().output
  finish({ errno: 0, stdout: "old saved log", stderr: "" })
  await reading
  assert.equal(h.state().savedLogVisible, false)
  assert.equal(h.state().output, current)
  assert.equal(h.state().savedOutput, "old saved log")
})
