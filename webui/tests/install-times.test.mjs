import assert from "node:assert/strict"
import { readFileSync, mkdtempSync, writeFileSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { spawnSync } from "node:child_process"
import { runInNewContext } from "node:vm"
import { test } from "node:test"
import ts from "typescript"

function load(exec = async () => { throw Error("Unexpected exec") }) {
  const exports = {}
  runInNewContext(ts.transpileModule(readFileSync(new URL("../src/lib/installTimes.ts", import.meta.url), "utf8"),
    { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } }).outputText,
  { exports, require: () => ({ exec }), AbortController })
  return exports
}
const helper = load()
const dump = `Packages:
  Package [com.alpha] (123):
    firstInstallTime=2024-05-06 07:08:09
    lastUpdateTime=2025-06-07 01:02:03
    User 0: installed=true
    User 10: installed=true
      firstInstallTime=2025-02-03 04:05:06.123 +08:00
    User 11: installed=false
      firstInstallTime=2025-02-04 04:05:06 UTC
  Package [com.beta] (456):
    User 0: installed=true firstInstallTime=2025-03-04 05:06:07Z enabled=0
    User 10: installed=true
      firstInstallTime=2025-04-05 06:07:08 -0530
Hidden system packages:
  Package [com.alpha] (789):
    firstInstallTime=2010-01-01 00:00:00
`

test("timestamps preserve spaces, fractions, zones and per-user installation dates", () => {
  const result = helper.parseInstallTimes(dump)
  assert.equal(result.get("com.alpha"), new Date(2024, 4, 6, 7, 8, 9).getTime())
  assert.equal(result.get("com.alpha@0"), result.get("com.alpha"))
  assert.equal(result.get("com.alpha@10"), Date.parse("2025-02-03T04:05:06.123+08:00"))
  assert.equal(result.has("com.alpha@11"), false)
  assert.equal(result.get("com.beta"), Date.parse("2025-03-04T05:06:07Z"))
  assert.equal(result.get("com.beta@10"), Date.parse("2025-04-05T06:07:08-05:30"))
  assert.equal(result.size, 6)
})

test("invalid or missing dates stay unknown and malformed package sections do not inherit state", () => {
  for (const stamp of ["2025-02-30 00:00:00", "2025-13-01 00:00:00", "2025-01-01 24:00:00",
    "2025-01-01 00:00:60", "2025-01-01 00:00:00 +24:00", "0", "2025-01-01", "2025-01-01 00:00:00 broken"]) {
    assert.equal(helper.parseInstallTimes("Package [com.app]\n  firstInstallTime=" + stamp).size, 0, stamp)
  }
  const data = helper.parseInstallTimes(`Package [com.valid]
  User 10: installed=true
    firstInstallTime=2025-01-01 00:00:00 UTC
Package [bad;$(touch /tmp/unsafe)]
  firstInstallTime=2025-02-01 00:00:00 UTC
Package [com.empty]
  lastUpdateTime=2025-03-01 00:00:00 UTC`)
  assert.equal(data.size, 1)
  assert.equal(data.get("com.valid@10"), Date.parse("2025-01-01T00:00:00Z"))
  assert.equal(data.has("com.valid"), false)
  for (const raw of ["", "Permission Denial", "DUMP TIMEOUT", "x".repeat(1_048_577)])
    assert.throws(() => helper.parseInstallTimes(raw), error => error.code === "output")
})

test("readInstallTimes executes once, passes cancellation and rejects every operational failure", async () => {
  const calls = [], controller = new AbortController()
  const h = load(async (...args) => { calls.push(args); return { errno: 0, stdout: dump, stderr: "" } })
  assert.equal((await h.readInstallTimes(controller.signal)).get("com.beta"), Date.parse("2025-03-04T05:06:07Z"))
  assert.equal(calls.length, 1)
  assert.match(calls[0][0], /dumpsys package packages /)
  assert.equal(calls[0][2], 15_000)
  assert.equal(calls[0][3], controller.signal)
  controller.abort()
  await assert.rejects(h.readInstallTimes(controller.signal), error => error.code === "aborted")
  assert.equal(calls.length, 1)
  for (const failure of ["unavailable", "bridge", "timeout"]) {
    const broken = load(async () => ({ errno: -1, stdout: dump, stderr: "", failure }))
    await assert.rejects(broken.readInstallTimes(), error => error.code === failure)
  }
  await assert.rejects(load(async () => ({ errno: 1, stdout: dump })).readInstallTimes(), error => error.code === "command")
  await assert.rejects(load(async () => { throw Error("Bridge rejected") }).readInstallTimes(), error => error.code === "bridge")
  const late = new AbortController()
  const cancelled = load(async () => { late.abort(); return { errno: 0, stdout: dump } })
  await assert.rejects(cancelled.readInstallTimes(late.signal), error => error.code === "aborted")
})

const turn = () => new Promise(resolve => setImmediate(resolve))
function cacheHarness() {
  let clock = 0
  const reads = []
  const cache = helper.createInstallTimesCache(signal => new Promise((resolve, reject) => {
    reads.push({ signal, resolve, reject })
  }), () => clock)
  return { ...cache, reads, advance: milliseconds => { clock += milliseconds } }
}
const data = value => new Map([["com.alpha", value]])

test("cache shares in-flight reads and isolates subscriber cancellation and returned maps", async () => {
  const h = cacheHarness(), first = new AbortController(), second = new AbortController()
  const one = h.get("bin/backend", "com.alpha", first.signal)
  const two = h.get("bin/backend", "com.alpha", second.signal)
  await turn()
  assert.equal(h.reads.length, 1)
  first.abort()
  await assert.rejects(one, error => error.code === "aborted")
  assert.equal(h.reads[0].signal.aborted, false)
  h.reads[0].resolve(data(10))
  const result = await two
  result.set("com.alpha", 999)
  assert.equal((await h.get("bin/backend", "com.alpha")).get("com.alpha"), 10)
  assert.equal(h.reads.length, 1)
  const aborted = new AbortController()
  aborted.abort()
  await assert.rejects(h.get("other", "other", aborted.signal), error => error.code === "aborted")
  assert.equal(h.reads[0].signal.aborted, false, "an already cancelled caller cannot evict a valid cache entry")
})

test("detached reads may finish to warm the cache and a returning page can rejoin before completion", async () => {
  const h = cacheHarness(), controller = new AbortController()
  const leaving = h.get("same", "packages", controller.signal)
  await turn()
  controller.abort()
  await assert.rejects(leaving, error => error.code === "aborted")
  assert.equal(h.reads[0].signal.aborted, false)
  const returning = h.get("same", "packages")
  h.reads[0].resolve(data(11))
  assert.equal((await returning).get("com.alpha"), 11)
  assert.equal(h.reads.length, 1)

  h.invalidate()
  const detached = new AbortController()
  const cancelled = h.get("same", "packages", detached.signal)
  await turn()
  detached.abort()
  await assert.rejects(cancelled, error => error.code === "aborted")
  h.reads[1].resolve(data(12))
  await turn()
  assert.equal((await h.get("same", "packages")).get("com.alpha"), 12)
  assert.equal(h.reads.length, 2, "a read with no remaining subscribers may populate the single cache slot")
})

test("cache TTL starts on completion and expires at thirty seconds without extending on hits", async () => {
  const h = cacheHarness()
  const first = h.get("same", "packages")
  await turn()
  h.advance(25_000)
  h.reads[0].resolve(data(10))
  await first
  h.advance(29_999)
  assert.equal((await h.get("same", "packages")).get("com.alpha"), 10)
  assert.equal(h.reads.length, 1)
  h.advance(1)
  const expired = h.get("same", "packages")
  const shared = h.get("same", "packages")
  await turn()
  assert.equal(h.reads.length, 2)
  h.reads[1].resolve(data(20))
  assert.equal((await expired).get("com.alpha"), 20)
  assert.equal((await shared).get("com.alpha"), 20)
})

test("single-slot context and package eviction rejects stale results even if the reader ignores abort", async () => {
  for (const [context, fingerprint] of [["other/backend", "packages"], ["same", "new packages"]]) {
    const h = cacheHarness()
    const old = h.get("same", "packages")
    const oldRejected = assert.rejects(old, error => error.code === "aborted")
    await turn()
    const next = h.get(context, fingerprint)
    await turn()
    await oldRejected
    assert.equal(h.reads[0].signal.aborted, true)
    h.reads[1].resolve(data(20))
    await next
    h.reads[0].resolve(data(999))
    await turn()
    assert.equal((await h.get(context, fingerprint)).get("com.alpha"), 20)
    const returning = h.get("same", "packages")
    await turn()
    assert.equal(h.reads.length, 3, "evicted contexts do not accumulate in a Map")
    h.reads[2].resolve(data(30))
    assert.equal((await returning).get("com.alpha"), 30)
  }
})

test("explicit invalidation refreshes unchanged package names and late failures cannot evict the new entry", async () => {
  const h = cacheHarness()
  const old = h.get("same", "packages")
  const rejected = assert.rejects(old, error => error.code === "aborted")
  await turn()
  h.invalidate()
  await rejected
  const next = h.get("same", "packages")
  await turn()
  h.reads[1].resolve(data(20))
  await next
  h.reads[0].reject(Error("late old failure"))
  await turn()
  assert.equal((await h.get("same", "packages")).get("com.alpha"), 20)
  h.invalidate()
  const refreshed = h.get("same", "packages")
  await turn()
  h.reads[2].resolve(data(30))
  assert.equal((await refreshed).get("com.alpha"), 30)
})

test("failed, empty and internally cancelled reads never become cached successes", async () => {
  const h = cacheHarness()
  for (const failure of ["reject", "empty", "abort"]) {
    const request = h.get("same", "packages")
    const rejected = assert.rejects(request)
    await turn()
    const read = h.reads.at(-1)
    if (failure === "reject") read.reject(Error("read failed"))
    if (failure === "empty") read.resolve(new Map())
    if (failure === "abort") { h.invalidate(); read.resolve(data(999)) }
    await rejected
  }
  const retry = h.get("same", "packages")
  await turn()
  assert.equal(h.reads.length, 4)
  h.reads[3].resolve(data(40))
  assert.equal((await retry).get("com.alpha"), 40)
})

test("shell filter invokes dumpsys with correct arguments, bounds output and preserves failure", () => {
  const dir = mkdtempSync(join(tmpdir(), "keybox-install-times-"))
  try {
    const script = join(dir, "dumpsys")
    writeFileSync(script, `#!/bin/sh
[ "$#" = 2 ] && [ "$1" = package ] && [ "$2" = packages ] || exit 90
printf '%s\\n' "$TEST_DUMP"
exit "\${TEST_STATUS:-0}"
`, { mode: 0o700 })
    const run = (data, status = "0") => spawnSync("sh", ["-c", helper.installTimesCommand()], {
      env: { ...process.env, PATH: dir + ":" + process.env.PATH, TEST_DUMP: data, TEST_STATUS: status }, encoding: "utf8",
    })
    const result = run(dump)
    assert.equal(result.status, 0, result.stderr)
    assert.doesNotMatch(result.stdout, /lastUpdateTime|DONE|Packages:/)
    assert.deepEqual([...helper.parseInstallTimes(result.stdout)], [...helper.parseInstallTimes(dump)])
    assert.notEqual(run(dump, "7").status, 0, "pipeline must retain dumpsys failure")
    assert.notEqual(run(dump + "\nDUMP TIMEOUT").status, 0, "partial dumps must fail even with exit code zero")
    assert.throws(() => helper.parseInstallTimes(dump + "\nDUMP TIMEOUT"), error => error.code === "output")
    assert.notEqual(run("Package [com.app]\nfirstInstallTime=" + "x".repeat(5000)).status, 0)
    // Generate output through the fake process to avoid environment argument limits.
    writeFileSync(script, "#!/bin/sh\nawk 'BEGIN { for (i=0;i<40000;i++) print \"Package [com.app] (123456789012345678901234567890):\" }'\n", { mode: 0o700 })
    const overflow = run("")
    assert.notEqual(overflow.status, 0)
    assert.ok(overflow.stdout.length <= 1_048_576)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})
