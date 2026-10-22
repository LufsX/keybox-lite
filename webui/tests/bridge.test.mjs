import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { createRequire } from "node:module"
import { spawnSync } from "node:child_process"
import { test } from "node:test"
import { createContext, runInContext } from "node:vm"
import ts from "typescript"

const require = createRequire(import.meta.url)
const sdkSource = readFileSync(require.resolve("kernelsu"), "utf8")
const bridgeSource = readFileSync(new URL("../src/lib/bridge.ts", import.meta.url), "utf8")

function harness(t, start) {
  t.mock.timers.enable({ apis: ["setTimeout"] })
  const timers = new Set()
  const calls = []
  const listenerErrors = []
  const host = {}
  if (start !== null) {
    host.ksu = {
      spawn(...args) {
        const child = host[args[3]]
        calls.push({ args, child })
        start?.(child, ...args)
      },
    }
  }
  const context = createContext({
    window: host,
    ksu: host.ksu,
    Error,
    console: { error: (...args) => listenerErrors.push(args), log() {} },
    setTimeout(fn, ms) {
      const id = setTimeout(() => {
        timers.delete(id)
        fn()
      }, ms)
      timers.add(id)
      return id
    },
    clearTimeout(id) {
      timers.delete(id)
      clearTimeout(id)
    },
  })
  let sdk
  function load(source) {
    const { outputText } = ts.transpileModule(source, {
      compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.CommonJS },
    })
    const module = { exports: {} }
    runInContext(`(function(require, module, exports) { ${outputText}\n})`, context)(
      (name) => {
        assert.equal(name, "kernelsu")
        return sdk
      }, module, module.exports,
    )
    return module.exports
  }
  // Execute the actual locked SDK in the same host, rather than guessing its event shape.
  sdk = load(sdkSource)
  const bridge = load(bridgeSource)
  return {
    sdk, exec: bridge.exec, host, calls, timers, listenerErrors,
    callbacks: () => Object.keys(host).filter((name) => name !== "ksu"),
  }
}

test("missing or unsupported native spawn resolves immediately without resources", async (t) => {
  const h = harness(t, null)
  for (const native of [undefined, {}]) {
    h.host.ksu = native
    const r = await h.exec("true")
    assert.equal(r.errno, -1)
    assert.equal(r.failure, "unavailable")
    assert.equal(h.timers.size, 0)
    assert.deepEqual(h.callbacks(), [])
  }
})

test("locked SDK loses synchronous startup errors; bridge captures and cleans them", async (t) => {
  const h = harness(t, () => { throw new Error("spawn denied") })
  let observed = false
  const child = h.sdk.spawn("true", [])
  child.on("error", () => { observed = true })
  assert.equal(observed, false, "SDK emits the error before returning its child")
  const r = await h.exec("true")
  assert.equal(r.errno, -1)
  assert.equal(r.failure, "bridge")
  assert.match(r.stderr, /spawn denied/)
  assert.equal(h.timers.size, 0)
  assert.deepEqual(h.callbacks(), [])
})

test("native protocol streams asynchronous output in order and preserves shell status", async (t) => {
  const h = harness(t)
  const lines = []
  const pending = h.exec("cat <<'END'\nhello\nEND", (line) => lines.push(line))
  const { args, child } = h.calls[0]
  assert.equal(args[0], "(\ncat <<'END'\nhello\nEND\n)\n")
  assert.deepEqual(JSON.parse(args[1]), [])
  assert.deepEqual(JSON.parse(args[2]), {})
  await Promise.resolve()
  child.stdout.emit("data", "first")
  child.stderr.emit("data", "warning")
  child.stdout.emit("data", "last")
  child.emit("exit", 2)
  const r = await pending
  assert.deepEqual(lines, ["first", "warning", "last"])
  assert.equal(r.stdout, "first\nlast\n")
  assert.equal(r.stderr, "warning\n")
  assert.equal(r.errno, 2)
  assert.equal(r.failure, undefined)
  assert.equal(h.timers.size, 0)
  assert.deepEqual(h.callbacks(), [])
  child.emit("error", "late error")
  child.stdout.emit("data", "late output")
  assert.deepEqual(lines, ["first", "warning", "last"])
})

test("handlers exist even if native spawn reports events synchronously", async (t) => {
  const h = harness(t, (child) => {
    child.stdout.emit("data", "ready")
    child.emit("exit", 0)
  })
  const r = await h.exec("true")
  assert.equal(r.errno, 0)
  assert.equal(r.stdout, "ready\n")
  assert.equal(h.timers.size, 0)
  assert.deepEqual(h.callbacks(), [])
})

test("asynchronous bridge error preserves output and ignores subsequent events", async (t) => {
  const h = harness(t)
  const pending = h.exec("true")
  const { child } = h.calls[0]
  child.stderr.emit("data", "partial diagnostic")
  child.emit("error", new Error("native failure"))
  child.emit("exit", 0)
  const r = await pending
  assert.equal(r.errno, -1)
  assert.equal(r.failure, "bridge")
  assert.match(r.stderr, /partial diagnostic\nnative failure/)
  assert.equal(h.timers.size, 0)
  assert.deepEqual(h.callbacks(), [])
})

test("null exit status cannot be presented as successful verification", async (t) => {
  const h = harness(t)
  const pending = h.exec("verify")
  h.calls[0].child.emit("exit", null)
  const r = await pending
  assert.equal(r.errno, -1)
  assert.equal(r.failure, "bridge")
  assert.equal(h.timers.size, 0)
})

test("timeout releases callback and timer; late output cannot affect the next command", async (t) => {
  const h = harness(t)
  const lines = []
  const pending = h.exec("slow", (line) => lines.push(line))
  const { child } = h.calls[0]
  child.stdout.emit("data", "before timeout")
  t.mock.timers.tick(14_999)
  assert.equal(h.callbacks().length, 1)
  t.mock.timers.tick(1)
  const r = await pending
  assert.equal(r.failure, "timeout")
  assert.equal(r.errno, -1)
  assert.equal(r.stdout, "before timeout\n")
  assert.equal(h.timers.size, 0)
  assert.deepEqual(h.callbacks(), [])

  const next = h.exec("next")
  assert.notEqual(h.calls[0].args[3], h.calls[1].args[3])
  child.stdout.emit("data", "after timeout")
  child.emit("exit", 0)
  h.calls[1].child.emit("exit", 0)
  assert.equal((await next).errno, 0)
  assert.deepEqual(lines, ["before timeout"])
  assert.equal(h.timers.size, 0)
  assert.deepEqual(h.callbacks(), [])
})

test("explicit long deadline allows a quiet command beyond the short default", async (t) => {
  const h = harness(t)
  const pending = h.exec("network command", undefined, 75 * 60_000)
  t.mock.timers.tick(74 * 60_000)
  assert.equal(h.callbacks().length, 1)
  h.calls[0].child.emit("exit", 0)
  assert.equal((await pending).errno, 0)
  assert.equal(h.timers.size, 0)
  assert.deepEqual(h.callbacks(), [])
})

test("a throwing output listener cannot prevent exit or cleanup", async (t) => {
  const h = harness(t)
  const pending = h.exec("true", () => { throw new Error("render failed") })
  h.calls[0].child.stdout.emit("data", "hello")
  h.calls[0].child.emit("exit", 0)
  assert.equal((await pending).errno, 0)
  assert.equal(h.listenerErrors.length, 1)
  assert.equal(h.timers.size, 0)
  assert.deepEqual(h.callbacks(), [])
})

// libsu submits commands to a managed shell and reads status after the job.
// Exercise an actual shell: callback-only mocks cannot detect an early shell exit.
test("explicit exit preserves the managed shell and initialization can continue", async (t) => {
  const marker = "__KEYBOX_JOB_STATUS__="
  function job(command) {
    return spawnSync("sh", [], {
      input: command + " \nprintf '" + marker + "%s\\n' \"$?\"\n",
      encoding: "utf8",
    })
  }
  const resolveDir = 'for d in /data/adb/modules/keybox-lite; do printf \"%s\\n\" \"$d\"; exit 0; done; exit 1'
  assert.equal(job(resolveDir).stdout.includes(marker), false,
    "unwrapped exit kills the shell before its status marker")
  const h = harness(t, (child, command) => {
    const result = job(command)
    assert.equal(result.error, undefined)
    const boundary = result.stdout.lastIndexOf(marker)
    assert.ok(boundary >= 0, "managed shell must survive the job")
    const output = result.stdout.slice(0, boundary).replace(/\n$/, "")
    if (output) for (const line of output.split("\n")) child.stdout.emit("data", line)
    if (result.stderr) child.stderr.emit("data", result.stderr.trimEnd())
    child.emit("exit", Number(result.stdout.slice(boundary + marker.length).trim()))
  })
  const dir = await h.exec(resolveDir)
  assert.equal(dir.errno, 0)
  assert.equal(dir.stdout.trim(), "/data/adb/modules/keybox-lite")
  const info = await h.exec('printf \'{"exists":true,"serials":[]}\'')
  assert.equal(info.errno, 0)
  assert.equal(JSON.parse(info.stdout).exists, true)
  assert.equal((await h.exec("exit 2")).errno, 2)
  assert.equal((await h.exec("false || exit $?; echo unreachable")).errno, 1)
  assert.equal((await h.exec("cat <<'END'\nhello\nEND")).stdout, "hello\n")
  assert.equal((await h.exec("printf ready # trailing comment")).stdout, "ready\n")
  assert.equal(h.timers.size, 0)
  assert.deepEqual(h.callbacks(), [])
})
