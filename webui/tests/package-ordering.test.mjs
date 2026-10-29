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
const { visiblePackages, createTargetPackageDirectory, orderTargetPackages, filterTargetPackages } = load("targets.ts", { "./appSort": sort })
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

test("prepared directory survives selection, mode and policy changes and updates missing package membership", () => {
  const prepare = createTargetPackageDirectory()
  const config = structuredClone(draft)
  const directory = prepare(status, config)
  config.targets = [{ package_name: "com.a", mode: "hack" }]
  config.system_apps = ["com.sys"]
  config.default_policy = { os_patch: "prop" }
  assert.equal(prepare(status, config), directory)
  const refreshed = { packages: packages.map(item => ({ ...item, selected: !item.selected })) }
  assert.equal(prepare(refreshed, config), directory, "identical status snapshots and backend selection changes keep the prepared directory")
  const projected = filterTargetPackages(directory, config, "", "selected", labels, true)
  assert.deepEqual(names(projected), ["com.a"])
  assert.equal(projected[0].mode, "hack")
  config.targets.push({ package_name: "com.missing@10", mode: "generate" }, { package_name: "uid:10042", mode: "hack" })
  const extended = prepare(status, config)
  assert.notEqual(extended, directory)
  assert.equal(extended.length, directory.length + 2)
  assert.equal(prepare(status, config), extended)
  config.targets[1].mode = "hack"
  assert.equal(prepare(status, config), extended)
  assert.equal(filterTargetPackages(extended, config, "missing", "all")[0].mode, "hack")
  config.system_apps.push("com.missing@10")
  const tracked = prepare(status, config)
  assert.notEqual(tracked, extended)
  assert.equal(filterTargetPackages(tracked, config, "missing", "all", labels, false).length, 0)
  config.targets.splice(1, 1)
  assert.equal(prepare(status, config).some(item => item.package_name === "com.missing@10"), false)
  assert.notEqual(prepare({ packages: [...packages] }, config), tracked)
})

test("prepared name order filters and projects draft state without another comparison", () => {
  let comparisons = 0
  const h = load("targets.ts", { "./appSort": { compareAppNames(a, b) { comparisons++; return sort.compareAppNames(a, b) } } })
  const config = structuredClone(draft)
  const directory = h.createTargetPackageDirectory()(status, config)
  const ordered = h.orderTargetPackages(directory, labels, "name")
  assert.ok(comparisons > 0)
  const preparedComparisons = comparisons
  for (const query of ["", "ALPHA", "com.", "tracked", "missing", "   ", "not installed"]) {
    for (const filter of ["listed", "all", "selected", "system", "user"]) {
      config.targets = query ? [{ package_name: "com.a", mode: "hack" }] : structuredClone(draft.targets)
      h.filterTargetPackages(ordered, config, query, filter, labels, false)
      assert.equal(comparisons, preparedComparisons)
    }
  }
  assert.deepEqual(names(h.filterTargetPackages(ordered, config, "", "all", labels, false)), ["com.z", "com.a"])
  // A genuine metadata change is allowed to prepare a new order.
  h.orderTargetPackages(directory, new Map([["com.a", { label: "Aardvark" }]]), "name")
  assert.ok(comparisons > preparedComparisons)
})

test("prepared explicit sorts retain legacy results across query, filters, missing targets, UID and users", () => {
  const snapshot = structuredClone(status)
  const config = {
    ...structuredClone(draft),
    targets: [...draft.targets, { package_name: "com.missing", mode: "hack" }, { package_name: "com.a@010", mode: "generate" }, { package_name: "uid:10001", mode: "hack" }],
  }
  const prepare = createTargetPackageDirectory()
  const directory = prepare(status, config)
  const metadata = new Map([...labels, ["com.a@010", { label: "Work Alpha", installedAt: 400 }]])
  for (const sort of ["name", "package", "installed"]) {
    const ordered = orderTargetPackages(directory, metadata, sort)
    for (const query of ["", "ALPHA", "com.", "@010", "uid:", "not found"]) {
      for (const filter of ["listed", "all", "selected", "system", "user"]) {
        for (const showSystem of [undefined, false, true]) {
          assert.deepEqual(
            filterTargetPackages(ordered, config, query, filter, metadata, showSystem),
            visiblePackages(status, config, query, filter, metadata, pinned, sort, showSystem),
          )
        }
      }
    }
  }
  const recent = filterTargetPackages(orderTargetPackages(directory, metadata, "installed"), config, "", "all", metadata, false)
  assert.deepEqual(names(recent), ["com.a@010", "com.a", "com.z", "uid:10001"])
  assert.equal(recent[0].installed, false)
  assert.equal(recent[0].mode, "generate")
  assert.deepEqual(status, snapshot, "preparing and ordering never mutate the native package order")
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
