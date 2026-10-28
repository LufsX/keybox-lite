import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { runInNewContext } from "node:vm"
import { test } from "node:test"
import ts from "typescript"

const source = readFileSync(new URL("../src/lib/apps.ts", import.meta.url), "utf8")
function harness(native, host = true) {
  const exports = {}, calls = []
  let now = 0
  runInNewContext(ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } }).outputText, {
    exports, Date: { now: () => now }, window: host ? { ksu: { getPackagesInfo() {} } } : {}, setTimeout,
    require() { return { getPackagesInfo(names) { calls.push([...names]); return native(names) } } },
  })
  return { ...exports, calls, advance(ms) { now += ms } }
}
test("native labels and icons are batched, validated, and mapped to multi-user targets", async () => {
  const h = harness(names => [
    ...names.map(packageName => ({ packageName, appLabel: " App " + packageName + " " })),
    { packageName: "foreign.app", appLabel: "Foreign" },
  ])
  const names = Array.from({ length: 50 }, (_, i) => "com.app" + i)
  const data = await h.packagesInfo([...names, "com.app0@10", "uid:10042", "com.bad/path"])
  assert.deepEqual(h.calls.map(batch => batch.length), [24, 24, 2])
  assert.equal(data.get("com.app0").label, "App com.app0")
  assert.equal(data.get("com.app0@10").icon, "ksu://icon/com.app0")
  assert.equal(data.has("foreign.app"), false)
  assert.equal(data.has("uid:10042"), false)
  assert.equal(data.has("com.bad/path"), false)
})
test("unsupported and failing hosts keep package fallbacks available", async () => {
  const absent = harness(() => { throw Error("must not run") }, false)
  assert.equal((await absent.packagesInfo(["com.app"])).size, 0)
  assert.equal(absent.calls.length, 0)
  const broken = harness(() => { throw Error("unavailable") })
  assert.equal((await broken.packagesInfo(["com.app"])).size, 0)
  const noLabel = harness(() => [{ packageName: "com.app", error: "unknown" }])
  const value = (await noLabel.packagesInfo(["com.app"])).get("com.app")
  assert.equal(value.label, "com.app")
  assert.equal(value.icon, "ksu://icon/com.app")
})
test("cancelled metadata requests stop before native work and between batches", async () => {
  const controller = new AbortController()
  controller.abort()
  const h = harness(() => [])
  assert.equal((await h.packagesInfo(["com.app"], controller.signal)).size, 0)
  assert.equal(h.calls.length, 0)
  const active = new AbortController()
  const next = harness(names => { active.abort(); return names.map(packageName => ({ packageName, appLabel: "Label" })) })
  const result = await next.packagesInfo(Array.from({ length: 30 }, (_, i) => "com.app" + i), active.signal)
  assert.equal(next.calls.length, 1)
  assert.equal(result.size, 0)
})

test("metadata reuses recent labels across pages and users, expires, and publishes batches", async () => {
  const h = harness(names => names.map(packageName => ({ packageName, appLabel: "App " + packageName })))
  const names = Array.from({ length: 50 }, (_, i) => "com.cache" + i)
  const snapshots = []
  await h.packagesInfo(names, undefined, value => snapshots.push(value))
  assert.equal(snapshots[0].size, 24, "first batch appears before all metadata finishes")
  assert.equal(snapshots.at(-1).size, 50)
  assert.equal(snapshots[0].size, 24, "published maps are snapshots")
  const count = h.calls.length
  const cached = await h.packagesInfo(["com.cache0@10", "com.cache1"])
  assert.equal(h.calls.length, count)
  assert.equal(cached.get("com.cache0@10").label, "App com.cache0")
  const aborted = new AbortController()
  aborted.abort()
  assert.equal((await h.packagesInfo(["com.cache0"], aborted.signal)).size, 0)
  h.advance(30_001)
  await h.packagesInfo(["com.cache0"])
  assert.equal(h.calls.length, count + 1)
})

test("unresolved metadata is retried and cancellation does not publish a late batch", async () => {
  let available = false
  const h = harness(names => available ? names.map(packageName => ({ packageName, appLabel: "Resolved" })) : [])
  await h.packagesInfo(["com.retry"])
  available = true
  assert.equal((await h.packagesInfo(["com.retry"])).get("com.retry").label, "Resolved")
  assert.equal(h.calls.length, 2)
  const controller = new AbortController()
  const cancelled = harness(() => { controller.abort(); return [] })
  let updates = 0
  await cancelled.packagesInfo(["com.cancel"], controller.signal, () => updates++)
  assert.equal(updates, 0)
})
