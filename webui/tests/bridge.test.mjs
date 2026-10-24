import assert from "node:assert/strict"
import { readFileSync, mkdtempSync, writeFileSync, existsSync, rmSync, mkdirSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { setImmediate, setTimeout as delay } from "node:timers/promises"
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
    AbortController,
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
    sdk, exec: bridge.exec, execUpdate: bridge.execUpdate, host, calls, timers, listenerErrors,
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

test("early stop waits for readiness and bounded polling reports status safely", async (t) => {
  let stopAttempts = 0
  let completed = false
  const h = harness(t, (child, command) => {
    if (command.includes("mktemp")) child.stdout.emit("data", "/data/local/tmp/keybox-webui.testing")
    else if (command.includes("read pid start")) {
      if (++stopAttempts === 1) child.stdout.emit("data", "STARTING")
      else completed = true
    } else if (command.includes("tail -c")) {
      child.stdout.emit("data", completed ? "130" : "RUNNING")
      child.stdout.emit("data", completed ? "final log" : "first log")
    }
    child.emit("exit", 0)
  })
  const controller = new AbortController()
  let stop
  const output = []
  const pending = h.execUpdate("sh action.sh", (text) => output.push(text), 60_000, controller.signal, (fn) => { stop = fn })
  await setImmediate()
  assert.deepEqual(output, ["first log\n"])
  const stopping = stop()
  await setImmediate()
  assert.equal(stopAttempts, 1)
  t.mock.timers.tick(100)
  await setImmediate()
  assert.equal((await stopping).errno, 0)
  assert.equal(stopAttempts, 2)
  t.mock.timers.tick(500)
  await setImmediate()
  assert.equal((await pending).errno, 130)
  assert.equal(output.at(-1), "final log\n")
  assert.equal(h.timers.size, 0)
})

test("monitor timeout, malformed status and destruction clean polling resources", async (t) => {
  for (const mode of ["timeout", "malformed", "destroy"]) {
    const h = harness(t, (child, command) => {
      if (command.includes("mktemp")) child.stdout.emit("data", "/data/local/tmp/keybox-webui.testing")
      else if (command.includes("tail -c")) {
        child.stdout.emit("data", mode === "malformed" ? "oops" : "RUNNING")
        child.stdout.emit("data", "partial")
      }
      child.emit("exit", 0)
    })
    const controller = new AbortController()
    let observations = 0
    const pending = h.execUpdate("sh action.sh", () => observations++, mode === "timeout" ? 0 : 60_000, controller.signal, () => {})
    await setImmediate()
    if (mode === "destroy") {
      assert.equal(h.timers.size, 1)
      controller.abort()
    }
    const result = await pending
    assert.equal(result.errno, -1)
    assert.equal(result.failure, mode === "timeout" ? "timeout" : "bridge")
    assert.equal(h.timers.size, 0)
    assert.deepEqual(h.callbacks(), [])
    assert.ok(h.calls.at(-1).args[0].includes("rm -rf"))
    const previous = observations
    t.mock.timers.tick(60_000)
    assert.equal(observations, previous)
    t.mock.timers.reset()
  }
})

test("disposing a command releases its native callback immediately", async (t) => {
  const h = harness(t)
  const controller = new AbortController()
  const pending = h.exec("waiting", undefined, 15_000, controller.signal)
  controller.abort()
  assert.equal((await pending).failure, "bridge")
  assert.equal(h.timers.size, 0)
  assert.deepEqual(h.callbacks(), [])
})

test("detached updates release a serial shell and preserve real supervisor cancellation", async (t) => {
  const root = mkdtempSync(join(tmpdir(), "keybox-bridge-"))
  t.after(() => {
    // Cancel validates this fixture record\'s PID/start token before signalling
    // its managed process group, including when an earlier assertion failed.
    const record = join(root, "state", "update-job.json")
    if (existsSync(record)) spawnSync(join(root, "keyboxlite"), ["cancel-update", join(root, "state")], { timeout: 5000 })
    writeFileSync(join(root, "finish"), "")
    rmSync(root, { recursive: true, force: true })
  })
  const binary = join(root, "keyboxlite")
  const built = spawnSync("go", ["build", "-o", binary, "."], {
    cwd: new URL("../../src", import.meta.url), encoding: "utf8",
  })
  assert.equal(built.status, 0, built.stderr)
  const temp = join(root, "tmp")
  mkdirSync(temp)
  const script = join(root, "action.sh")
  const gate = join(root, "finish")
  const state = join(root, "state")
  writeFileSync(script, "#!/bin/sh\ncount=0\nwhile [ ! -f '" + gate + "' ]; do\n[ -d '" + root + "' ] || exit 9\ncount=$((count + 1))\n[ \"$count\" -lt 200 ] || exit 9\nsleep 0.05\ndone\necho final-output\nexit 7\n")
  // Synchronous jobs model a serial native queue. A foreground update would
  // hold this shell until the two-second test deadline fails.
  const h = harness(t, (child, command) => {
    const result = spawnSync("sh", [], {
      input: command.replaceAll("/data/local/tmp", temp),
      encoding: "utf8", timeout: 2000,
    })
    assert.equal(result.error, undefined, "native shell must return before completion")
    for (const line of result.stdout.trimEnd().split("\n").filter(Boolean)) {
      child.stdout.emit("data", line.replaceAll(temp, "/data/local/tmp"))
    }
    if (result.stderr) child.stderr.emit("data", result.stderr.trimEnd())
    child.emit("exit", result.status)
  })
  const command = "'" + binary + "' run-update '" + script + "' '" + state + "'"
  let stop
  const outputs = []
  const pending = h.execUpdate(command, (text) => outputs.push(text), 60_000, new AbortController().signal, (fn) => { stop = fn }, state)
  await setImmediate()
  const targetsRoot = join(root, "device")
  mkdirSync(targetsRoot)
  const targets = await h.exec("\'" + binary + "\' targets --root \'" + targetsRoot + "\' status")
  assert.equal(targets.errno, 0, targets.stderr)
  assert.equal(typeof JSON.parse(targets.stdout), "object")
  assert.equal(existsSync(gate), false)
  assert.ok(stop)
  for (let i = 0; i < 100 && !existsSync(join(state, "update-job.json")); i++) await delay(10)
  assert.equal(existsSync(join(state, "update-job.json")), true)
  const other = h.execUpdate(command, () => {}, 60_000, new AbortController().signal, () => {})
  let otherResult
  void other.then((r) => { otherResult = r })
  for (let i = 0; i < 100 && !otherResult; i++) {
    await delay(10)
    t.mock.timers.tick(500)
    await setImmediate()
  }
  assert.equal(otherResult?.errno, 1, "supervisor rejects a concurrent update")
  const stopResult = stop()
  let stopped
  void stopResult.then((r) => { stopped = r })
  for (let i = 0; i < 40 && !stopped; i++) {
    await delay(10)
    t.mock.timers.tick(100)
    await setImmediate()
  }
  assert.equal(stopped?.errno, 0, JSON.stringify(stopped))
  for (let i = 0; i < 100 && existsSync(join(state, "update-job.json")); i++) await delay(10)
  let ended
  void pending.then((r) => { ended = r })
  for (let i = 0; i < 100 && !ended; i++) {
    await delay(10)
    t.mock.timers.tick(500)
    await setImmediate()
  }
  assert.equal(ended?.errno, 130, JSON.stringify(ended))
  assert.equal(existsSync(join(state, "update-job.json")), false)
  assert.match(outputs.at(-1), /Stopping update/)
  assert.deepEqual(h.callbacks(), [])
  assert.equal(h.timers.size, 0)

  writeFileSync(gate, "")
  const finished = h.execUpdate(command, () => {}, 60_000, new AbortController().signal, () => {})
  let final
  void finished.then((r) => { final = r })
  for (let i = 0; i < 100 && !final; i++) {
    await delay(10)
    t.mock.timers.tick(500)
    await setImmediate()
  }
  assert.equal(final?.errno, 7)
  assert.match(final.stdout, /final-output/)
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
