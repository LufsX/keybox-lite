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
  { exports, require: () => ({ exec }) })
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
