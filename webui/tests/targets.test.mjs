import assert from "node:assert/strict"
import { readFileSync, mkdtempSync, writeFileSync, rmSync, existsSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { spawnSync } from "node:child_process"
import { createRequire } from "node:module"
import { test } from "node:test"
import { runInNewContext } from "node:vm"
import ts from "typescript"

const component = readFileSync(new URL("../src/lib/components/TargetManager.svelte", import.meta.url), "utf8")
const transpile = (source, module = ts.ModuleKind.CommonJS) => ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2020, module },
}).outputText
function load(path, deps = {}) {
  const exports = {}
  runInNewContext(transpile(readFileSync(new URL(path, import.meta.url), "utf8")), {
    exports,
    require(name) {
      if (deps[name]) return deps[name]
      throw new Error(name)
    },
  })
  return exports
}
const requireFromTest = createRequire(import.meta.url)
const tinyPinyin = requireFromTest("tiny-pinyin")
const appSort = load("../src/lib/appSort.ts", { "tiny-pinyin": { default: tinyPinyin, ...tinyPinyin } })
const targets = load("../src/lib/targets.ts", { "./appSort": appSort })
const locale = load("../src/lib/i18n.ts")
const ui = load("../src/lib/ui.ts")
const plain = value => JSON.parse(JSON.stringify(value))
const response = value => ({ errno: 0, stdout: JSON.stringify(value), stderr: "" })
function fixture(overrides = {}) {
  return {
    supported: true, backend: { backend: "tricky-store", identity: "TS", active: true },
    config: {
      targets: [{ package_name: "com.alpha", mode: "auto" }],
      default_policy: { os_patch: "202501", future_field: "keep this" },
      per_app_policy: { "com.alpha": { vendor_patch: "prop", extra: "preserved" } },
    },
    packages: [
      { package_name: "com.alpha", system: false, selected: true, mode: "auto", installed: true },
      { package_name: "com.beta", system: false, selected: false, mode: "generate", installed: true },
      { package_name: "com.system", system: true, selected: false, mode: "hack", installed: true },
    ],
    system_apps: [], auto_add_new_apps: false,
    entry: { enabled: false, module_id: "tricky_store", linked: false, has_own_webui: false },
    revision: "revision-1", ...overrides,
  }
}
function harness(initial = fixture()) {
  const calls = [], queue = [], effects = [], cleanups = [], confirmations = [], metadataQueue = [], metadataCalls = [], menuChanges = []
  const timers = new Map()
  let focused = 0
  class Element { closest() { return null } }
  const document = { activeElement: null, getElementById: () => ({ focus() { focused++ } }) }
  let current = initial, confirm = true
  const exports = {}
  const script = component.match(/<script lang="ts">([\s\S]*?)<\/script>/)[1]
  runInNewContext(transpile(script + `
    export const handlers = {
      entryChecked, entryStateLabel, canManageEntry, canLeave, refresh, save, setEntry, select, selectVisible, setMode, setPolicy, trackSystem, autoAdd, addManual, beforeUnload,
      openEditor, closeEditor, applyEditor, editPolicy, countChanges,
      toggleMenu, closeMenu, dismissMenu, menuKeydown, menuAction, openSheet, closeSheet, toggleSystem, applySystem, discard, loadMetadata,
      bindMenu: node => { menuElement = node },
      editorMode: mode => { editor.mode = mode },
      editorTracked: value => { editor.tracked = value },
      state: () => ({ status, draft, baseline, ready, entryReady, pending, error, notice, editor, menuOpen, sheet, systemDraft, metadata, dirty: changed() }),
      search: (text, kind = "all") => { query = text; filter = kind },
      // Mirror the component: rows are ordered by the selection captured on first load.
      visible: () => status && draft ? visiblePackages(status, draft, query, filter, metadata, initialSelection ?? undefined) : [],
      listed: () => status && draft ? visiblePackages(status, draft, query, "listed", metadata, initialSelection ?? undefined) : [],
      manual: (text, mode = "auto") => { manual = text; manualMode = mode; addManual() },
      context: (value, blocked = false) => { bin = value; disabled = blocked },
    }
  `), {
    exports,
    require(name) {
      if (name === "svelte") return { onDestroy: fn => cleanups.push(fn), untrack: fn => fn() }
      if (name === "../bridge") return { exec(command) {
        calls.push(command)
        return queue.length ? Promise.resolve(queue.shift()) : Promise.resolve(response(current))
      } }
      if (name === "../i18n") return locale
      if (name === "../targets") return targets
      if (name === "../ui") return ui
      if (name === "../apps") return { packagesInfo(names, signal) { metadataCalls.push({ names, signal }); return metadataQueue.length ? metadataQueue.shift() : Promise.resolve(new Map()) } }
      if (name === "../motion") return { surface: () => ({}), reveal: () => ({}), pageExit: () => ({}) }
      if (name === "./AppListSkeleton.svelte") return {}
      throw new Error(name)
    },
    $props: Object.assign(() => ({ bin: "/module/bin/keybox", lang: "en", onMenuChange: value => menuChanges.push(value) }), { id: () => "harness" }),
    AbortController, Element, document,
    setTimeout: fn => { const id = {}; timers.set(id, fn); return id },
    clearTimeout: id => timers.delete(id),
    $state: value => value,
    $derived: value => value,
    $effect: fn => effects.push(fn),
    window: { confirm(message) { confirmations.push(message); return confirm } },
  })
  const h = exports.handlers
  return {
    ...h, calls, confirmations, metadataCalls, menuChanges, Element, document,
    metadataResult: value => metadataQueue.push(value),
    expireMetadata: () => { for (const fn of [...timers.values()]) fn(); timers.clear() },
    timerCount: () => timers.size,
    focused: () => focused,
    queue: (...results) => queue.push(...results),
    status: value => { current = value },
    confirm: value => { confirm = value },
    effect: () => effects.forEach(fn => fn()),
    dispose: () => cleanups.forEach(fn => fn()),
  }
}

test("target tokens, dates and strict JSON validation preserve unknown safe policies", () => {
  for (const name of ["com.example", "android", "com.example@10", "uid:10001", "uid:0", "uid:001", "com.example@00", "_app", "com._app"])
    assert.equal(targets.validPackage(name), true, name)
  for (const name of ["10001", "uid:-1", "uid:1;id", "com.a@-1", "com.a\n", "com.a;id", "__proto__", "constructor", "target", "default_policy", "uid:4294967296", "com.a@4294967296"])
    assert.equal(targets.validPackage(name), false, name)
  assert.equal(targets.validPolicyValue("vendor_patch", "20240229"), true)
  for (const [key, value] of [["os_patch", "202513"], ["os_patch", "20250101"], ["boot_patch", "20250229"], ["vendor_patch", "20250431"]])
    assert.equal(targets.validPolicyValue(key, value), false)
  assert.equal(targets.validPolicy({ future: "x'$(printf harmless)" }), true)
  for (const policy of [{ future: 1 }, { future: "x\ny" }, { constructor: "bad" }, { "bad-key": "bad" }])
    assert.equal(targets.validPolicy(policy), false)
  assert.deepEqual(plain(targets.parseTargetStatus(JSON.stringify(fixture()))), fixture())
  const malformed = [
    null, {}, [], { ...fixture(), revision: "" }, { ...fixture(), supported: "true" },
    { ...fixture(), backend: { active: true } },
    { ...fixture(), entry: { enabled: 1, linked: false, has_own_webui: false, module_id: null } },
    { ...fixture(), packages: [{ ...fixture().packages[0], installed: "true" }] },
    { ...fixture(), packages: [fixture().packages[0], fixture().packages[0]] },
    { ...fixture(), config: { ...fixture().config, default_policy: { os_patch: "202599" } } },
    { ...fixture(), config_error: false },
  ]
  for (const value of malformed) assert.throws(() => targets.parseTargetStatus(JSON.stringify(value)))
  for (const raw of ["", "{}", '{"ok":false}', '{"ok":"true"}', '{"ok":true,"error":"failed"}', '{"ok":true}\nnoise'])
    assert.throws(() => targets.parseTargetAck(raw), raw)
  targets.parseTargetAck('{"ok":true}')
})

test("shell command sends exact JSON on stdin with quotes, substitutions and newlines inert", () => {
  const dir = mkdtempSync(join(tmpdir(), "keybox-targets-"))
  try {
    const bin = join(dir, "bin ' with space")
    const marker = join(dir, "injected")
    writeFileSync(bin, '#!/bin/sh\n[ "$1" = targets ] && [ "$2" = save ] || exit 6\ncat\n', { mode: 0o700 })
    const draft = targets.draftFromStatus(fixture())
    draft.default_policy.future_field = "quote'$(touch " + marker + ")"
    const revision = "rev'\n$(touch " + marker + ")"
    const result = spawnSync("sh", ["-c", targets.targetSaveCommand(bin, revision, draft)], { encoding: "utf8" })
    assert.equal(result.status, 0, result.stderr)
    assert.deepEqual(JSON.parse(result.stdout), { revision, ...plain(draft) })
    assert.equal(existsSync(marker), false)
    assert.equal(targets.targetStatusCommand("/a'b"), "'/a'\"'\"'b' targets status")
    assert.throws(() => targets.shellQuote("a\0b"))
  } finally { rmSync(dir, { recursive: true, force: true }) }
})

test("standalone page loads automatically once its context is available", async () => {
  const h = harness()
  h.context("")
  h.effect()
  assert.equal(h.calls.length, 0)
  h.context("/module/bin/keybox")
  h.effect()
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(h.calls.length, 1)
  assert.equal(h.calls[0], "'/module/bin/keybox' targets status")
  assert.equal(h.state().ready, true)
  assert.equal(h.state().notice, "")
  h.effect()
  assert.equal(h.calls.length, 1)
})

test("save carries all modes, policies, inherited blanks, system tracking and revision then reloads", async () => {
  const h = harness()
  await h.refresh(false)
  h.select("com.beta", true)
  h.select("com.system", true)
  h.setMode("com.alpha", "generate")
  h.setPolicy(null, "os_patch", "202603")
  h.setPolicy(null, "boot_patch", "no")
  h.setPolicy("com.alpha", "vendor_patch", "")
  h.setPolicy("com.alpha", "os_patch", "prop")
  h.setPolicy("com.beta", "boot_patch", "20240229")
  h.autoAdd(true)
  h.manual("uid:10005", "hack")
  h.manual("com.work@10", "auto")
  const expected = plain(h.state().draft)
  assert.deepEqual(expected.targets, [
    { package_name: "com.alpha", mode: "generate" },
    { package_name: "com.beta", mode: "generate" },
    { package_name: "com.system", mode: "hack" },
    { package_name: "uid:10005", mode: "hack" },
    { package_name: "com.work@10", mode: "auto" },
  ])
  assert.deepEqual(expected.default_policy, { os_patch: "202603", boot_patch: "no", future_field: "keep this" })
  assert.deepEqual(expected.per_app_policy["com.alpha"], { extra: "preserved", os_patch: "prop" })
  assert.deepEqual(expected.system_apps, ["com.system"])
  const fresh = fixture({ revision: "revision-2", config: {
    targets: expected.targets, default_policy: expected.default_policy, per_app_policy: expected.per_app_policy,
  }, system_apps: expected.system_apps, auto_add_new_apps: true })
  h.queue(response({ ok: true }), response(fresh))
  await h.save()
  assert.equal(h.calls[1], targets.targetSaveCommand("/module/bin/keybox", "revision-1", expected))
  assert.equal(h.calls[2], targets.targetStatusCommand("/module/bin/keybox"))
  assert.equal(h.state().dirty, false)
  assert.equal(h.state().status.revision, "revision-2")
  assert.equal(h.state().notice, locale.messages.en.targetsSaved)
})

test("search and user/system filters bound bulk selection and clearing to visible apps", async () => {
  const h = harness()
  await h.refresh(false)
  h.search("COM.", "system")
  assert.deepEqual(plain(h.visible()).map(item => item.package_name), ["com.system"])
  h.selectVisible(true)
  assert.deepEqual(plain(h.state().draft.system_apps), ["com.system"])
  h.search("beta", "user")
  h.selectVisible(true)
  h.setPolicy("com.beta", "os_patch", "no")
  h.selectVisible(false)
  assert.deepEqual(plain(h.state().draft.targets).map(item => item.package_name), ["com.alpha", "com.system"])
  assert.equal(h.state().draft.per_app_policy["com.beta"], undefined)
  h.search("", "system")
  h.selectVisible(false)
  // Clearing untargets the visible rows but keeps system tracking; only the dialog or the editor untracks.
  assert.deepEqual(plain(h.state().draft.system_apps), ["com.system"])
  h.manual("uid:12345", "auto")
  h.search("uid:")
  assert.equal(h.visible()[0].installed, false)
  h.manual("invalid;id")
  assert.equal(h.state().error, locale.messages.en.targetsInvalid)
})

test("failed save keeps edits and permits retry; failed load keeps edits and blocks saving", async () => {
  const h = harness()
  await h.refresh(false)
  h.setPolicy(null, "os_patch", "202602")
  const draft = plain(h.state().draft)
  h.queue({ errno: 1, stdout: '{"ok":true}', stderr: "revision conflict" })
  await h.save()
  assert.deepEqual(plain(h.state().draft), draft)
  assert.equal(h.state().dirty, true)
  assert.equal(h.state().pending, false)
  assert.match(h.state().error, /revision conflict/)
  assert.equal(h.state().ready, true)
  h.queue({ errno: 0, stdout: "not JSON", stderr: "" })
  await h.refresh(false)
  assert.equal(h.state().ready, false)
  assert.deepEqual(plain(h.state().draft), draft)
  const calls = h.calls.length
  await h.save()
  assert.equal(h.calls.length, calls)
})

test("malformed acknowledgement, bridge failure and failed post-save reload cannot report completion", async () => {
  for (const result of [
    response({ ok: false }), response({ ok: "true" }),
    { errno: 0, failure: "timeout", stdout: '{"ok":true}', stderr: "" },
    { errno: 0, failure: "unavailable", stdout: "", stderr: "" },
  ]) {
    const h = harness()
    await h.refresh(false)
    h.autoAdd(true)
    const draft = plain(h.state().draft)
    h.queue(result)
    await h.save()
    assert.ok(h.state().error)
    assert.equal(h.state().notice, "")
    assert.equal(h.state().dirty, true)
    assert.deepEqual(plain(h.state().draft), draft)
    assert.equal(h.calls.length, 2)
  }
  const h = harness()
  await h.refresh(false)
  h.autoAdd(true)
  h.queue(response({ ok: true }), response(fixture({ config_error: "broken target config" })))
  await h.save()
  assert.equal(h.state().ready, false)
  assert.equal(h.state().dirty, true)
  assert.equal(h.state().draft.auto_add_new_apps, true)
  assert.match(h.state().error, /broken target config/)
})

test("status config/package errors disable target mutations and save", async () => {
  for (const field of ["config_error", "packages_error"]) {
    const h = harness(fixture({ [field]: "read failed" }))
    await h.refresh(false)
    assert.equal(h.state().ready, false)
    h.select("com.beta", true)
    await h.save()
    assert.equal(h.calls.length, 1)
    assert.match(h.state().error, /read failed/)
  }
})

test("unsaved refresh asks before discarding and beforeunload protects navigation", async () => {
  const h = harness()
  await h.refresh(false)
  h.autoAdd(true)
  h.confirm(false)
  await h.refresh()
  assert.equal(h.calls.length, 1)
  assert.equal(h.state().dirty, true)
  assert.equal(h.confirmations.length, 1)
  let prevented = false
  const event = { preventDefault() { prevented = true } }
  h.beforeUnload(event)
  assert.equal(prevented, true)
  assert.equal(event.returnValue, "")
  h.confirm(true)
  await h.refresh()
  assert.equal(h.state().dirty, false)
})

test("leaving confirms dirty edits, blocks pending load/save/entry and recovers after completion", async () => {
  const h = harness()
  let finishLoad
  h.queue(new Promise(resolve => { finishLoad = resolve }))
  const loading = h.refresh(false)
  assert.equal(h.canLeave(), false)
  assert.equal(h.state().notice, locale.messages.en.targetsPending)
  assert.equal(h.confirmations.length, 0)
  finishLoad(response(fixture()))
  await loading
  assert.equal(h.canLeave(), true)
  h.autoAdd(true)
  h.confirm(false)
  assert.equal(h.canLeave(), false)
  assert.equal(h.state().dirty, true)
  assert.equal(h.confirmations.at(-1), locale.messages.en.targetsLeave)
  h.confirm(true)
  assert.equal(h.canLeave(), true)
  let finishSave
  h.queue(new Promise(resolve => { finishSave = resolve }))
  const saving = h.save()
  const prompts = h.confirmations.length
  assert.equal(h.canLeave(), false)
  assert.equal(h.confirmations.length, prompts)
  finishSave(response({ ok: true }))
  await saving
  assert.equal(h.canLeave(), true)
  let finishEntry
  h.queue(new Promise(resolve => { finishEntry = resolve }))
  const entry = h.setEntry(true)
  assert.equal(h.canLeave(), false)
  finishEntry({ errno: 1, stdout: "", stderr: "entry failed" })
  await entry
  assert.equal(h.canLeave(), true)
})

test("pending jobs exclude duplicate operations, stale paths and disabled parents; destroy ignores results", async () => {
  for (const end of ["path", "disabled", "destroy"]) {
    const h = harness()
    let resolve
    h.queue(new Promise(done => { resolve = done }))
    const pending = h.refresh(false)
    assert.equal(h.state().pending, true)
    await h.refresh(false)
    await h.save()
    await h.setEntry(true)
    assert.equal(h.calls.length, 1)
    if (end === "path") { h.context("/other/bin"); h.effect() }
    if (end === "disabled") { h.context("/module/bin/keybox", true); h.effect() }
    if (end === "destroy") h.dispose()
    resolve(response(fixture()))
    await pending
    if (end === "path") {
      assert.equal(h.state().status.revision, "revision-1")
      assert.equal(h.calls.length, 2) // The new page context loads automatically.
    } else {
      assert.equal(h.state().status, null)
      assert.equal(h.state().notice, "")
    }
    if (end !== "destroy") assert.equal(h.state().pending, false)
  }
})

test("a late old load cannot overwrite a newer context's successful load", async () => {
  const h = harness()
  let resolve
  h.queue(new Promise(done => { resolve = done }))
  const old = h.refresh(false)
  h.status(fixture({ revision: "new-revision" }))
  h.context("/new/bin")
  h.effect()
  await new Promise(resolve => setImmediate(resolve))
  resolve(response(fixture({ revision: "old-revision" })))
  await old
  assert.equal(h.state().status.revision, "new-revision")
  assert.equal(h.state().pending, false)
})

test("entry switches on then off its linked entry and operates on OMK/TEES without target support", async () => {
  for (const identity of ["TS", "OMK", "TEES"]) {
    const initial = fixture({ supported: identity === "TS", backend: { backend: identity, identity, active: true } })
    const h = harness(initial)
    await h.refresh(false)
    const linked = { ...initial.entry, enabled: true, linked: true }
    h.queue(response({ ok: true }), response({ ...initial, entry: linked }))
    await h.setEntry(true)
    assert.equal(h.calls[1], "'/module/bin/keybox' targets entry set on")
    assert.equal(h.state().status.entry.linked, true)
    assert.equal(h.state().notice, locale.messages.en.targetsEntryDone)
    h.queue(response({ ok: true }), response(initial))
    await h.setEntry(false)
    assert.equal(h.calls[3], "'/module/bin/keybox' targets entry set off")
    assert.equal(h.state().status.entry.enabled, false)
  }
})

test("entry conflicts block enabling, allow disabling, protect edits and surface failures", async () => {
  for (const field of ["has_own_webui"]) {
    const initial = fixture()
    initial.entry[field] = true
    const h = harness(initial)
    await h.refresh(false)
    await h.setEntry(true)
    assert.equal(h.calls.length, 1)
    h.queue(response({ ok: true }), response(initial))
    await h.setEntry(false)
    assert.equal(h.calls[1], "'/module/bin/keybox' targets entry set off")
  }
  const h = harness()
  await h.refresh(false)
  h.autoAdd(true)
  await h.setEntry(true)
  assert.equal(h.calls.length, 1)
  await h.refresh(false)
  h.queue({ errno: 1, stdout: '{"ok":true}', stderr: "entry conflict" })
  await h.setEntry(true)
  assert.match(h.state().error, /entry conflict/)
  assert.equal(h.state().status.entry.enabled, false)
  const inactive = harness(fixture({ backend: { backend: "TS", identity: "TS", active: false } }))
  await inactive.refresh(false)
  await inactive.setEntry(true)
  assert.equal(inactive.calls.length, 1)
})

test("an existing owned link can be enabled again after feature state is lost", async () => {
  const initial = fixture()
  initial.entry.linked = true
  const h = harness(initial)
  await h.refresh(false)
  h.queue(response({ ok: true }), response({ ...initial, entry: { ...initial.entry, enabled: true } }))
  await h.setEntry(true)
  assert.equal(h.calls[1], "'/module/bin/keybox' targets entry set on")
  assert.equal(h.state().status.entry.enabled, true)
  assert.equal(h.state().status.entry.linked, true)
  assert.equal(h.state().error, "")
})

test("entry cleanup survives inactive or missing backends and config/package errors", async () => {
  for (const overrides of [
    { backend: { backend: "tricky-store", identity: "TS", active: false } },
    { backend: null, supported: false },
    { config_error: "invalid config" },
    { packages_error: "pm unavailable" },
    { backend: null, supported: false, config_error: "invalid config", packages_error: "pm unavailable" },
  ]) {
    const initial = fixture(overrides)
    initial.entry = { ...initial.entry, enabled: true, linked: true }
    const h = harness(initial)
    await h.refresh(false)
    const cleaned = { ...initial, entry: { ...initial.entry, enabled: false, linked: false } }
    h.queue(response({ ok: true }), response(cleaned))
    await h.setEntry(false)
    assert.equal(h.calls[1], "'/module/bin/keybox' targets entry set off")
    assert.equal(h.state().status.entry.enabled, false)
    assert.equal(h.state().notice, locale.messages.en.targetsEntryDone)
  }
})

test("entry switch removes an orphan even when its saved setting is off", async () => {
  const initial = fixture()
  initial.entry.linked = true
  const h = harness(initial)
  await h.refresh(false)
  assert.equal(h.entryChecked(), true)
  h.queue(response({ ok: true }), response(fixture()))
  await h.setEntry(!h.entryChecked())
  assert.equal(h.calls[1], "'/module/bin/keybox' targets entry set off")
  assert.equal(h.entryChecked(), false)
  assert.equal(h.entryStateLabel(), locale.messages.en.targetsEntryOff)
})

test("entry failures reload partial changes and never optimistically toggle the switch", async () => {
  const initial = fixture()
  initial.entry = { ...initial.entry, enabled: true, linked: true }
  const h = harness(initial)
  await h.refresh(false)
  let finish
  h.queue(new Promise(resolve => { finish = resolve }), response(initial))
  const off = h.setEntry(false)
  assert.equal(h.entryChecked(), true)
  assert.equal(h.canManageEntry(false), false)
  finish({ errno: 1, stdout: "", stderr: "unlink denied" })
  await off
  assert.equal(h.entryChecked(), true)
  assert.match(h.state().error, /unlink denied/)
  assert.equal(h.state().notice, "")
  // A command can fail after removing the links but before persisting the setting.
  const partial = { ...initial, entry: { ...initial.entry, linked: false } }
  h.queue({ errno: 1, stdout: "", stderr: "state write failed" }, response(partial))
  await h.setEntry(false)
  assert.equal(h.state().status.entry.linked, false)
  assert.equal(h.entryStateLabel(), locale.messages.en.targetsEntryWaiting)
  assert.match(h.state().error, /state write failed/)
  assert.equal(h.canManageEntry(false), true)
})

test("entry acknowledgement must match real links and an unreadable reload marks status unknown", async () => {
  const h = harness()
  await h.refresh(false)
  const orphan = fixture()
  orphan.entry.linked = true
  h.queue(response({ ok: true }), response(orphan))
  await h.setEntry(false)
  assert.equal(h.entryChecked(), true)
  assert.ok(h.state().error)
  assert.equal(h.state().notice, "")
  h.queue(response({ ok: true }), { errno: 1, stdout: "", stderr: "read failed" })
  await h.setEntry(false)
  assert.equal(h.state().entryReady, false)
  assert.equal(h.entryStateLabel(), locale.messages.en.targetsEntryUnknown)
  assert.equal(h.canManageEntry(false), false)
  assert.equal(h.state().notice, "")
  await h.refresh(false)
  assert.equal(h.entryChecked(), false)
  assert.equal(h.canManageEntry(true), true)
})

test("TS-L keeps global policies and modes while rejecting per-app edits and UID system tracking", async () => {
  const initial = fixture({
    backend: { backend: "tricky-store-legacy", identity: "TS-L", active: true },
    config: { ...fixture().config, per_app_policy: {} },
  })
  const h = harness(initial)
  await h.refresh(false)
  h.setMode("com.alpha", "hack")
  h.setPolicy(null, "boot_patch", "prop")
  h.setPolicy("com.alpha", "os_patch", "202603")
  h.manual("uid:12345", "generate")
  h.trackSystem("uid:12345", true)
  h.trackSystem("com.alpha", true)
  assert.deepEqual(plain(h.state().draft.per_app_policy), {})
  assert.deepEqual(plain(h.state().draft.system_apps), [])
  const expected = plain(h.state().draft)
  h.queue(response({ ok: true }), response(initial))
  await h.save()
  assert.equal(h.calls[1], targets.targetSaveCommand("/module/bin/keybox", initial.revision, expected))
  assert.deepEqual(expected.per_app_policy, {})
  assert.equal(expected.targets[0].mode, "hack")
  assert.equal(expected.default_policy.boot_patch, "prop")
})

test("stale save completion and failed entry reload cannot overwrite state or claim success", async () => {
  const h = harness()
  await h.refresh(false)
  h.autoAdd(true)
  let resolve
  h.queue(new Promise(done => { resolve = done }))
  const pending = h.save()
  await h.save()
  assert.equal(h.calls.length, 2)
  h.status(fixture({ revision: "fresh" }))
  h.context("/other/bin")
  h.effect()
  await new Promise(resolve => setImmediate(resolve))
  resolve(response({ ok: true }))
  await pending
  assert.equal(h.state().status.revision, "fresh")
  assert.equal(h.calls.length, 3)
  h.queue(response({ ok: true }), response(fixture())) // Entry remains off despite successful command exit.
  await h.setEntry(true)
  assert.ok(h.state().error)
  assert.equal(h.state().notice, "")
  assert.equal(h.state().ready, false)
})

test("selected filtering and row mode always follow draft changes rather than loaded selection", async () => {
  const h = harness()
  await h.refresh(false)
  h.search("", "selected")
  assert.deepEqual(plain(h.visible()).map(item => item.package_name), ["com.alpha"])
  h.select("com.beta", true)
  h.setMode("com.beta", "hack")
  assert.deepEqual(plain(h.visible()).map(item => [item.package_name, item.mode, item.selected]), [
    ["com.alpha", "auto", true], ["com.beta", "hack", true],
  ])
  h.search("BETA", "selected")
  h.selectVisible(false)
  assert.equal(h.visible().length, 0)
  h.search("", "selected")
  assert.equal(h.visible().length, 1)
  h.select("com.alpha", false)
  assert.equal(h.visible().length, 0)
  h.search("", "all")
  assert.equal(h.visible().find(item => item.package_name === "com.alpha").selected, false)
})

test("mode editor isolates changes, cancels cleanly and applies mode, policy and system tracking to the draft", async () => {
  const h = harness()
  await h.refresh(false)
  const original = plain(h.state().draft)
  h.openEditor("com.beta")
  assert.equal(h.state().editor, null) // Only selected rows expose editing.
  h.openEditor("com.alpha")
  assert.deepEqual(plain(h.state().editor.policy), original.per_app_policy["com.alpha"])
  h.editorMode("hack")
  h.editPolicy("os_patch", "202603")
  assert.deepEqual(plain(h.state().draft), original)
  h.confirm(false)
  assert.equal(h.canLeave(), false)
  let prevented = false
  h.beforeUnload({ preventDefault() { prevented = true } })
  assert.equal(prevented, true)
  h.closeEditor()
  assert.equal(h.state().dirty, false)
  h.openEditor("com.alpha")
  assert.equal(h.state().editor.mode, "auto")
  assert.equal(h.state().editor.policy.os_patch, undefined)
  h.editorMode("generate")
  h.editPolicy("vendor_patch", "")
  h.editPolicy("boot_patch", "20250229")
  h.applyEditor()
  assert.ok(h.state().editor) // Invalid dates cannot enter the draft.
  h.editPolicy("boot_patch", "20240229")
  h.applyEditor()
  assert.equal(h.state().editor, null)
  assert.equal(h.state().draft.targets[0].mode, "generate")
  assert.deepEqual(plain(h.state().draft.per_app_policy["com.alpha"]), { extra: "preserved", boot_patch: "20240229" })
  assert.equal(h.countChanges(), 1)
  h.select("com.system", true)
  h.openEditor("com.system")
  assert.equal(h.state().editor.tracked, true)
  h.editorTracked(false)
  h.applyEditor()
  assert.deepEqual(plain(h.state().draft.system_apps), [])
  assert.equal(h.countChanges(), 2)
  h.openEditor("com.alpha")
  await h.save()
  assert.equal(h.calls.length, 1) // Resolve local edits before writing to the backend.
  h.closeEditor()
  const expected = plain(h.state().draft)
  h.queue(response({ ok: true }), response(fixture({ config: {
    targets: expected.targets, default_policy: expected.default_policy, per_app_policy: expected.per_app_policy,
  }, system_apps: expected.system_apps, revision: "saved" })))
  await h.save()
  assert.equal(h.calls[1], targets.targetSaveCommand("/module/bin/keybox", "revision-1", expected))
  assert.equal(h.state().dirty, false)
  assert.equal(h.countChanges(), 0)
})

test("mode editor cannot apply stale, disabled or legacy per-app edits", async () => {
  for (const change of ["disabled", "path"]) {
    const h = harness()
    await h.refresh(false)
    h.openEditor("com.alpha")
    h.editorMode("hack")
    h.context(change === "path" ? "/other/bin" : "/module/bin/keybox", change === "disabled")
    h.effect()
    h.applyEditor()
    assert.equal(h.state().editor, null)
  }
  const h = harness(fixture({ backend: { backend: "tricky-store-legacy", identity: "TS-L", active: true } }))
  await h.refresh(false)
  h.openEditor("com.alpha")
  h.editorMode("hack")
  h.editPolicy("os_patch", "no")
  h.applyEditor()
  assert.equal(h.state().draft.targets[0].mode, "hack")
  assert.deepEqual(plain(h.state().draft.per_app_policy), {})
})

test("dirty count counts affected applications once and includes global settings", async () => {
  const h = harness()
  await h.refresh(false)
  assert.equal(h.countChanges(), 0)
  h.setMode("com.alpha", "hack")
  h.setPolicy("com.alpha", "os_patch", "prop")
  assert.equal(h.countChanges(), 1)
  h.setPolicy(null, "boot_patch", "no")
  h.autoAdd(true)
  assert.equal(h.countChanges(), 3)
  h.autoAdd(false)
  h.setPolicy(null, "boot_patch", "")
  h.setMode("com.alpha", "auto")
  h.setPolicy("com.alpha", "os_patch", "")
  assert.equal(h.countChanges(), 0)
})


test("the selection captured on first load keeps draft toggles and reloads from reordering rows", async () => {
  const initial = fixture()
  initial.config.targets = [{ package_name: "com.zulu", mode: "hack" }, { package_name: "com.system", mode: "auto" }]
  initial.packages.push({ package_name: "com.zulu", system: false, selected: true, mode: "hack", installed: true })
  const h = harness(initial)
  await h.refresh(false)
  const names = () => plain(h.visible()).map(item => item.package_name)
  assert.deepEqual(names(), ["com.system", "com.zulu", "com.alpha", "com.beta"])
  h.select("com.zulu", false)
  h.select("com.beta", true)
  assert.deepEqual(names(), ["com.system", "com.zulu", "com.alpha", "com.beta"])
  h.search("", "selected")
  assert.deepEqual(names(), ["com.system", "com.beta"])
  h.search("", "all")
  const draft = plain(h.state().draft)
  h.queue(response({ ok: true }), response({ ...initial, revision: "saved", config: {
    targets: draft.targets, default_policy: draft.default_policy, per_app_policy: draft.per_app_policy,
  }, system_apps: draft.system_apps }))
  await h.save()
  assert.deepEqual(names(), ["com.system", "com.zulu", "com.alpha", "com.beta"])
  await h.refresh()
  assert.equal(h.state().notice, locale.messages.en.targetsRefreshed)
})


test("system tracking dialog applies only on confirmation and keeps selected system targets visible", async () => {
  const h = harness()
  await h.refresh(false)
  assert.deepEqual(plain(h.listed()).map(item => item.package_name), ["com.alpha", "com.beta"])
  h.openSheet("system")
  h.toggleSystem("com.system")
  assert.equal(h.state().draft.system_apps.length, 0)
  h.closeSheet()
  assert.equal(h.state().dirty, false)
  h.openSheet("system")
  assert.deepEqual(plain(h.state().systemDraft), [])
  h.toggleSystem("com.system")
  h.applySystem()
  assert.ok(h.listed().some(item => item.package_name === "com.system"))
  h.select("com.system", true)
  h.openSheet("system")
  h.toggleSystem("com.system")
  h.applySystem()
  assert.deepEqual(plain(h.state().draft.system_apps), [])
  assert.ok(h.listed().some(item => item.package_name === "com.system"))
  h.menuAction("clear")
  assert.equal(h.state().draft.targets.length, 0)
  // Clear only untargets the visible rows; the tracked system app stays listed.
  assert.deepEqual(plain(h.state().draft.system_apps), ["com.system"])
  assert.ok(h.listed().some(item => item.package_name === "com.system"))
})

test("menu callback, trigger exclusion, keyboard focus, outside dismissal and action closing stay consistent", async () => {
  const h = harness()
  await h.refresh(false)
  const inside = new h.Element(), outside = new h.Element(), trigger = new h.Element()
  trigger.closest = selector => selector === "#targets-menu-trigger" ? trigger : null
  const buttons = [0, 1, 2].map(index => ({ index, focus() { h.document.activeElement = this } }))
  h.bindMenu({ contains: node => node === inside, querySelectorAll: () => buttons })
  h.toggleMenu()
  h.effect()
  assert.equal(h.menuChanges.at(-1), true)
  h.dismissMenu({ target: trigger })
  h.dismissMenu({ target: inside })
  assert.equal(h.state().menuOpen, true)
  const key = value => h.menuKeydown({ key: value, preventDefault() {} })
  key("ArrowDown")
  assert.equal(h.document.activeElement, buttons[0])
  key("End")
  assert.equal(h.document.activeElement, buttons[2])
  key("ArrowDown")
  assert.equal(h.document.activeElement, buttons[0])
  key("ArrowUp")
  assert.equal(h.document.activeElement, buttons[2])
  key("Escape")
  h.effect()
  assert.equal(h.menuChanges.at(-1), false)
  assert.equal(h.focused(), 1)
  h.toggleMenu()
  h.dismissMenu({ target: outside })
  assert.equal(h.state().menuOpen, false)
  h.toggleMenu()
  h.menuAction("auto")
  assert.equal(h.state().menuOpen, false)
  assert.equal(h.state().draft.auto_add_new_apps, true)
})

test("metadata searches labels and ignores errors, timed out responses, stale contexts and unmounted results", async () => {
  const h = harness()
  await h.refresh(false)
  let resolveOld
  h.metadataResult(new Promise(resolve => { resolveOld = resolve }))
  const stopOld = h.loadMetadata(["com.alpha"], "/module/bin/keybox")
  stopOld()
  h.metadataResult(Promise.resolve(new Map([["com.alpha", { label: "Alpha Bank", icon: "ksu://icon/com.alpha" }]])))
  const stop = h.loadMetadata(["com.alpha"], "/module/bin/keybox")
  await new Promise(resolve => setImmediate(resolve))
  resolveOld(new Map([["com.alpha", { label: "Old name", icon: null }]]))
  await new Promise(resolve => setImmediate(resolve))
  h.search("BANK")
  assert.equal(h.listed()[0].package_name, "com.alpha")
  assert.equal(h.state().metadata.get("com.alpha").label, "Alpha Bank")
  assert.equal(h.timerCount(), 0)
  stop()
  for (const end of ["timeout", "path", "destroy", "error"]) {
    const current = harness()
    await current.refresh(false)
    let resolve
    current.metadataResult(end === "error" ? Promise.reject(new Error("unavailable")) : new Promise(done => { resolve = done }))
    const cleanup = current.loadMetadata(["com.alpha"], "/module/bin/keybox")
    if (end === "timeout") current.expireMetadata()
    if (end === "path") current.context("/other/bin")
    if (end === "destroy") current.dispose()
    if (resolve) resolve(new Map([["com.alpha", { label: "stale", icon: null }]]))
    await new Promise(done => setImmediate(done))
    assert.equal(current.state().metadata.size, 0)
    assert.equal(current.state().ready, true)
    cleanup()
    assert.equal(current.metadataCalls[0].signal.aborted, true)
    assert.equal(current.timerCount(), 0)
  }
})
