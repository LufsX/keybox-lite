import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { runInNewContext } from "node:vm"
import { test } from "node:test"
import ts from "typescript"
import Pinyin from "tiny-pinyin"

function load(file, deps, globals = {}) {
  const exports = {}
  runInNewContext(ts.transpileModule(readFileSync(new URL("../src/lib/" + file, import.meta.url), "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText, { exports, require: name => deps[name], ...globals })
  return exports
}
const sort = load("appSort.ts", { "tiny-pinyin": { default: Pinyin } })
const { visiblePackages } = load("targets.ts", { "./appSort": sort })
const names = rows => Array.from(rows, row => row.package_name)
const packages = [
  { package_name: "com.z", system: false, selected: true, installed: true, mode: "auto" },
  { package_name: "com.a", system: false, selected: false, installed: true, mode: "auto" },
  { package_name: "com.sys", system: true, selected: true, installed: true, mode: "auto" },
  { package_name: "com.tracked", system: true, selected: false, installed: true, mode: "auto" },
  { package_name: "com.hidden", system: true, selected: false, installed: true, mode: "auto" },
]
const status = { packages }
const draft = { targets: [packages[0], packages[2]], system_apps: ["com.tracked", "com.missing"] }
const labels = new Map([
  ["com.z", { label: "Alpha", installedAt: 100 }],
  ["com.a", { label: "Zebra", installedAt: 300 }],
])
const pinned = new Set(["com.z", "com.sys"])
const list = (sort, showSystem, filter = "listed", metadata = labels, config = draft) =>
  visiblePackages(status, config, "", filter, metadata, pinned, sort, showSystem)

test("explicit name, package and installed sorts outrank pinned and selected priorities", () => {
  assert.deepEqual(names(list("package", false)), ["com.a", "com.z"])
  assert.deepEqual(names(list("name", false)), ["com.z", "com.a"])
  assert.deepEqual(names(list("installed", false)), ["com.a", "com.z"])
  const reversedLabels = new Map([["com.z", { label: "Zulu" }], ["com.a", { label: "Alpha" }]])
  assert.deepEqual(names(list("name", false, "listed", reversedLabels)), ["com.a", "com.z"])
  assert.deepEqual(names(list(undefined, false, "listed", reversedLabels)), ["com.z", "com.a"])
})

test("unknown, invalid and tied install times use name ordering after all known dates", () => {
  const metadata = new Map([
    ["com.z", { label: "Zulu", installedAt: NaN }],
    ["com.a", { label: "Alpha", installedAt: null }],
    ["com.sys", { label: "System", installedAt: 200 }],
    ["com.tracked", { label: "Tracked", installedAt: Infinity }],
    ["com.hidden", { label: "Hidden", installedAt: -1 }],
  ])
  assert.deepEqual(names(list("installed", true, "all", metadata)),
    ["com.sys", "com.a", "com.hidden", "com.tracked", "com.z"])
  metadata.set("com.z", { label: "Zulu", installedAt: 200 })
  assert.deepEqual(names(list("installed", true, "all", metadata)).slice(0, 2), ["com.sys", "com.z"])
})

test("explicit system hiding applies to all filters, including selected, tracked and missing packages", () => {
  const config = { ...draft, targets: [...draft.targets, { package_name: "com.missing", mode: "auto" }] }
  for (const filter of ["listed", "all", "selected", "system"]) {
    const rows = list("name", false, filter, labels, config)
    assert.equal(rows.some(item => item.system), false, filter)
    assert.equal(rows.some(item => item.package_name === "com.missing"), false, filter)
  }
  assert.ok(names(list("name", true, "all", labels, config)).includes("com.hidden"))
  // Omission retains legacy system dialog and listed behavior.
  assert.deepEqual(new Set(names(list(undefined, undefined, "system"))),
    new Set(["com.sys", "com.tracked", "com.hidden"]))
  assert.ok(names(list(undefined, undefined)).includes("com.sys"))
  assert.ok(names(list(undefined, undefined)).includes("com.tracked"))
  assert.equal(names(list(undefined, undefined)).includes("com.hidden"), false)
})

test("native metadata never treats uncontracted install time fields as reliable dates", async () => {
  const apps = load("apps.ts", { kernelsu: {
    getPackagesInfo: () => [{ packageName: "com.app", appLabel: "App", firstInstallTime: 999, installTime: 888 }],
  } }, { window: { ksu: { getPackagesInfo() {} } }, setTimeout })
  const result = await apps.packagesInfo(["com.app", "com.app@10"])
  assert.equal(result.get("com.app").label, "App")
  assert.equal(result.get("com.app").installedAt, null)
  assert.equal(result.get("com.app@10").installedAt, null)
})
