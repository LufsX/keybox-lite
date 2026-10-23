import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { runInNewContext } from "node:vm"
import { test } from "node:test"
import ts from "typescript"

const source = readFileSync(new URL("../src/lib/apps.ts", import.meta.url), "utf8")
function harness(native, host = true) {
  const exports = {}, calls = []
  runInNewContext(ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } }).outputText, {
    exports, window: host ? { ksu: { getPackagesInfo() {} } } : {}, setTimeout,
    require() { return { getPackagesInfo(names) { calls.push([...names]); return native(names) } } },
  })
  return { ...exports, calls }
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
