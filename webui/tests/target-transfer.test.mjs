import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { runInNewContext } from "node:vm"
import { test } from "node:test"
import ts from "typescript"

function load(path, dependencies = {}) {
  const exports = {}
  const source = ts.transpileModule(readFileSync(new URL(path, import.meta.url), "utf8"), {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
  }).outputText
  runInNewContext(source, { exports, TextEncoder, require: name => {
    if (name in dependencies) return dependencies[name]
    throw Error(name)
  } })
  return exports
}
const targets = load("../src/lib/targets.ts", { "./appSort": {} })
const transfer = load("../src/lib/targetTransfer.ts", { "./targets": targets })
const messages = load("../src/lib/targetTransferMessages.ts")
const plain = value => JSON.parse(JSON.stringify(value))
const backend = { backend: "tricky-store", identity: "TS" }
function draft() {
  return {
    targets: [{ package_name: "com.zulu", mode: "hack" }, { package_name: "com.alpha", mode: "auto" }],
    default_policy: { os_patch: "202602", vendor_patch: "20260228" },
    per_app_policy: { "com.zulu": { boot_patch: "no" } },
    system_apps: ["com.system"], auto_add_new_apps: true,
  }
}
function status(destination = backend) {
  return { supported: true, backend: { ...destination, active: true }, packages: [
    { package_name: "com.alpha", installed: true }, { package_name: "com.zulu", installed: true },
    { package_name: "com.work@10", installed: true },
  ] }
}
const encode = (value = draft()) => transfer.exportTargetTransfer(value, backend)
function error(text, code = "data", current) {
  assert.throws(() => transfer.parseTargetTransfer(text, current), err => err.code === code)
}
test("roundtrip exports only explicit editable fields, preserves array order, excludes backend details", () => {
  const source = draft()
  source.default_policy.native_secret = "not exported"
  source.per_app_policy["com.zulu"].opaque = "not exported"
  const data = JSON.parse(transfer.exportTargetTransfer(source, { ...backend, module_id: "private", revision: "private", path: "/secret" }))
  assert.equal(data.format, "keybox-lite.targets")
  assert.equal(data.version, 1)
  assert.deepEqual(data.backend, backend)
  assert.deepEqual(data.targets, draft().targets)
  assert.deepEqual(data.default_policy, draft().default_policy)
  assert.deepEqual(data.per_app_policy, draft().per_app_policy)
  assert.deepEqual(Object.keys(data), ["format", "version", "backend", "targets", "default_policy", "per_app_policy", "system_apps", "auto_add_new_apps"])
  const parsed = transfer.parseTargetTransfer(JSON.stringify(data))
  const preview = transfer.previewTargetTransfer(parsed, draft(), status())
  assert.deepEqual(plain(preview.draft), draft())
  assert.ok(Object.values(preview.changes).every(count => count === 0))
})
test("file size is checked before reading and UTF-8 byte limit also applies to text", async () => {
  let reads = 0
  await assert.rejects(transfer.readTargetTransfer({ size: 1048577, text() { reads++; throw Error() } }), err => err.code === "size")
  assert.equal(reads, 0)
  error(" ".repeat(1048577), "size")
  error("汉".repeat(350000), "size")
  await assert.rejects(transfer.readTargetTransfer({ size: -1, text() { reads++ } }), err => err.code === "size")
  assert.equal(reads, 0)
})
test("rejects malformed JSON, format, version and unknown root or backend fields", () => {
  error("{", "json")
  error("null", "format")
  error("[]", "format")
  const data = JSON.parse(encode())
  for (const value of ["foreign", null]) error(JSON.stringify({ ...data, format: value }), "format")
  for (const value of [0, 2, "1", null]) error(JSON.stringify({ ...data, version: value }), "version")
  for (const extra of [{ revision: "x" }, { path: "/native" }, { unknown: {} }]) error(JSON.stringify({ ...data, ...extra }))
  error(JSON.stringify({ ...data, backend: { ...backend, active: true } }))
  error(JSON.stringify({ ...data, backend: { backend: "oh-my-keymint", identity: "TS" } }))
})
test("rejects duplicate targets, canonical UID/user aliases, system apps and duplicate JSON keys", () => {
  const data = JSON.parse(encode())
  for (const names of [["com.a", "com.a"], ["uid:1", "uid:001"], ["com.a@10", "com.a@010"]])
    error(JSON.stringify({ ...data, targets: names.map(package_name => ({ package_name, mode: "auto" })) }))
  for (const names of [["com.a", "com.a"], ["com.a@0", "com.a@00"], ["uid:1"]])
    error(JSON.stringify({ ...data, system_apps: names }))
  error(encode().replace('"version":1', '"version":1,"version":1'))
  error(encode().replace('"os_patch":"202602"', '"os_patch":"202602","os_patch":"prop"'))
  error(JSON.stringify({ ...data, targets: [{ ...data.targets[0], native: "bad" }] }))
})
test("rejects prototype keys at every level, invalid modes, overlong names and strange patch values", () => {
  const data = JSON.parse(encode())
  for (const key of ["__proto__", "constructor", "prototype"]) {
    error(encode().replace('"version":1', '"version":1,"' + key + '":{}'))
    error(encode().replace('"os_patch":"202602"', '"' + key + '":"x"'))
    error(encode().replace('"com.zulu":{', '"' + key + '":{'))
  }
  for (const package_name of ["com.a;id", "com.a\n", "a".repeat(256), "uid:4294967296", "com.a@4294967296"])
    error(JSON.stringify({ ...data, targets: [{ package_name, mode: "auto" }] }))
  for (const mode of ["patch", "", {}, 1, null]) error(JSON.stringify({ ...data, targets: [{ package_name: "com.a", mode }] }))
  for (const policy of [{ os_patch: "202613" }, { os_patch: "189912" }, { vendor_patch: "20260229" }, { boot_patch: "20260431" }, { os_patch: 202601 }, { future: "injected" }, { boot_patch: "yes" }, { os_patch: "x".repeat(5000) }])
    error(JSON.stringify({ ...data, default_policy: policy }))
})
test("opaque native policies can only remain unchanged and never travel into another backend", () => {
  const current = draft()
  current.default_policy.future = "retain"
  const data = { ...JSON.parse(encode()), default_policy: { future: "retain" } }
  error(JSON.stringify(data))
  const parsed = transfer.parseTargetTransfer(JSON.stringify(data), current)
  const preview = transfer.previewTargetTransfer(parsed, current, status())
  assert.equal(preview.draft.default_policy.future, "retain")
  assert.equal(preview.warnings[0].code, "opaque")
  data.default_policy.future = "changed"
  error(JSON.stringify(data), "data", current)
  assert.ok(!encode(current).includes("future"))
})
test("backend conversion provides concrete warnings and preserves metadata and native global policy", () => {
  const data = transfer.parseTargetTransfer(encode())
  const current = draft()
  current.default_policy = { os_patch: "prop" }
  for (const [name, identity, hasModes] of [
    ["tricky-store-legacy", "TS-L", true], ["tee-simulator-rs", "TEES-RS", true],
    ["tee-simulator", "TEES", false], ["oh-my-keymint", "OMK", false],
  ]) {
    const preview = transfer.previewTargetTransfer(data, current, status({ backend: name, identity }))
    assert.deepEqual(plain(preview.draft.default_policy), hasModes ? plain(data.default_policy) : current.default_policy)
    assert.deepEqual(plain(preview.draft.per_app_policy), {})
    assert.equal(preview.draft.targets[0].mode, hasModes ? "hack" : "auto")
    assert.deepEqual(plain(preview.draft.system_apps), current.system_apps)
    assert.equal(preview.draft.auto_add_new_apps, true)
    assert.equal(preview.warnings.find(w => w.code === (hasModes ? "appPolicies" : "policies")).count, hasModes ? 1 : 3)
    assert.equal(preview.warnings.some(w => w.code === "modes"), !hasModes)
  }
})
test("all Go adapters retain UID/user targets without flattening their names", () => {
  const source = draft()
  source.targets.push({ package_name: "uid:10001", mode: "hack" }, { package_name: "com.work@10", mode: "auto" })
  source.per_app_policy["uid:10001"] = { boot_patch: "no" }
  const data = transfer.parseTargetTransfer(encode(source))
  for (const [backend, identity, excluded] of [
    ["tricky-store", "TS", []], ["tricky-store-legacy", "TS-L", []], ["tee-simulator-rs", "TEES-RS", []],
    ["tee-simulator", "TEES", []], ["oh-my-keymint", "OMK", []],
  ]) {
    const preview = transfer.previewTargetTransfer(data, draft(), status({ backend, identity }))
    assert.deepEqual(plain(preview.warnings.find(w => w.code === "identifiers")?.names ?? []), excluded)
    assert.deepEqual(plain(preview.draft.targets).map(t => t.package_name), source.targets.map(t => t.package_name))
  }
})
test("preview counts effective differences, missing apps and order without mutating either draft", () => {
  const current = draft(), next = draft()
  next.targets = [{ package_name: "com.alpha", mode: "generate" }, { package_name: "com.missing", mode: "auto" }]
  next.default_policy.os_patch = "prop"
  next.per_app_policy = {}
  next.system_apps = []
  next.auto_add_new_apps = false
  const original = JSON.stringify(current), imported = JSON.stringify(next)
  const preview = transfer.previewTargetTransfer(transfer.parseTargetTransfer(encode(next)), current, status())
  assert.deepEqual(plain(preview.changes), { added: 1, removed: 1, modes: 1, policies: 2, system: 1, auto: 1, order: 0 })
  assert.deepEqual(plain(preview.missing), ["com.missing"])
  assert.equal(JSON.stringify(current), original)
  assert.equal(JSON.stringify(next), imported)
  const reordered = draft()
  reordered.targets.reverse()
  assert.equal(transfer.previewTargetTransfer(transfer.parseTargetTransfer(encode(reordered)), current, status()).changes.order, 1)
})
test("all seven locales supply every transfer label, warning and error", () => {
  const dictionary = messages.targetTransferMessages
  assert.deepEqual(Object.keys(dictionary).sort(), ["en", "es", "ja", "ko", "ru", "zh", "zh-TW"])
  function validate(value, reference) {
    assert.deepEqual(Object.keys(value).sort(), Object.keys(reference).sort())
    for (const [key, item] of Object.entries(value)) {
      if (typeof item === "object") validate(item, reference[key])
      else assert.equal(typeof item === "string" && item.trim().length > 0, true)
    }
  }
  for (const locale of Object.values(dictionary)) validate(locale, dictionary.en)
})
