import assert from "node:assert/strict"
import { readFileSync, mkdtempSync, writeFileSync, existsSync, rmSync, mkdirSync, openSync, closeSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { setImmediate, setTimeout as delay } from "node:timers/promises"
import { createRequire } from "node:module"
import { getEventListeners } from "node:events"
import { spawnSync } from "node:child_process"
import { test } from "node:test"
import { createContext, runInContext } from "node:vm"
import ts from "typescript"

const require = createRequire(import.meta.url)
const sdkSource = readFileSync(require.resolve("kernelsu"), "utf8")
const bridgeSource = readFileSync(new URL("../src/lib/bridge.ts", import.meta.url), "utf8")

function harness(t, start, { document } = {}) {
  t.mock.timers.enable({ apis: ["setTimeout", "Date"] })
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
    ...(document ? { document } : {}),
    Date,
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

function visibilityDocument(visibilityState = "visible") {
  const listeners = new Set()
  return {
    visibilityState, listeners,
    addEventListener(type, listener) {
      assert.equal(type, "visibilitychange")
      listeners.add(listener)
    },
    removeEventListener(type, listener) {
      assert.equal(type, "visibilitychange")
      listeners.delete(listener)
    },
    change(state) {
      this.visibilityState = state
      for (const listener of [...listeners]) listener()
    },
  }
}

function reply(child, ...lines) {
  for (const line of lines) child.stdout.emit("data", line)
  child.emit("exit", 0)
}

function monitorHarness(t, { document, read, stop, launch } = {}) {
  const reads = []
  let launches = 0
  const h = harness(t, (child, command) => {
    if (command.includes("mktemp")) {
      if (launch) launch(child)
      else reply(child, "/data/local/tmp/keybox-webui.testing" + ++launches)
    } else if (command.includes("tail -c")) {
      reads.push({ child, command })
      read?.(child, reads.length, command)
    } else if (command.includes("read pid start") && stop) {
      stop(child)
    } else {
      reply(child)
    }
  }, { document })
  return { ...h, reads }
}

function assertMonitorClean(h, document, signal) {
  assert.equal(h.timers.size, 0)
  assert.deepEqual(h.callbacks(), [])
  if (document) assert.equal(document.listeners.size, 0)
  assert.equal(getEventListeners(signal, "abort").length, 0)
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

test("hidden monitor keeps early stop readiness retries at 100ms", async (t) => {
  const document = visibilityDocument("hidden")
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
  }, { document })
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
  assert.equal(h.calls.filter(({ args }) => args[0].includes("tail -c")).length, 1)
  document.change("visible")
  assert.equal((await pending).errno, 130)
  assert.equal(output.at(-1), "final log\n")
  assertMonitorClean(h, document, controller.signal)
})

test("unchanged log is published once while completion and invalid statuses are still processed", async (t) => {
  for (const status of ["0", "130", "oops", "256"]) {
    await t.test(status, async (t) => {
      const document = visibilityDocument()
      const h = monitorHarness(t, {
        document,
        read: (child, count) => reply(child, count < 3 ? "RUNNING" : status, "same log"),
      })
      const controller = new AbortController()
      const output = []
      const pending = h.execUpdate("synthetic-update", (text) => output.push(text), 60_000, controller.signal, () => {})
      await setImmediate()
      t.mock.timers.tick(500)
      await setImmediate()
      assert.equal(h.reads.length, 2)
      assert.deepEqual(output, ["same log\n"])
      t.mock.timers.tick(500)
      const result = await pending
      if (status === "0" || status === "130") {
        assert.equal(result.errno, Number(status))
        assert.equal(result.stdout, "same log\n")
        assert.equal(result.failure, undefined)
      } else {
        assert.equal(result.failure, "bridge")
        assert.match(result.stderr, /Invalid update completion status/)
      }
      assert.deepEqual(output, ["same log\n"])
      assertMonitorClean(h, document, controller.signal)
    })
  }
})

test("a polling-shell error cannot be mistaken for the detached operation exit status", async t => {
  const document = visibilityDocument()
  const h = monitorHarness(t, {
    document, read(child) { child.stderr.emit("data", "native polling failed"); child.emit("exit", 1) },
  })
  const controller = new AbortController()
  const result = await h.execUpdate("synthetic-update", () => {}, 60_000, controller.signal, () => {})
  assert.equal(result.errno, 1)
  assert.equal(result.failure, "bridge")
  assert.equal(result.stderr.trim(), "native polling failed")
  assertMonitorClean(h, document, controller.signal)
})

test("hidden polling waits 2500ms and becoming visible immediately restores 500ms polling", async (t) => {
  const document = visibilityDocument("hidden")
  const h = monitorHarness(t, { document, read: (child) => reply(child, "RUNNING", "log") })
  const controller = new AbortController()
  const pending = h.execUpdate("synthetic-update", () => {}, 60_000, controller.signal, () => {})
  await setImmediate()
  assert.equal(h.reads.length, 1)
  assert.equal(document.listeners.size, 1)
  t.mock.timers.tick(2499)
  await setImmediate()
  assert.equal(h.reads.length, 1)
  t.mock.timers.tick(1)
  await setImmediate()
  assert.equal(h.reads.length, 2)
  t.mock.timers.tick(100)
  document.change("visible")
  await setImmediate()
  assert.equal(h.reads.length, 3)
  t.mock.timers.tick(499)
  await setImmediate()
  assert.equal(h.reads.length, 3)
  t.mock.timers.tick(1)
  await setImmediate()
  assert.equal(h.reads.length, 4)
  controller.abort()
  assert.equal((await pending).failure, "bridge")
  assertMonitorClean(h, document, controller.signal)
  const calls = h.calls.length
  document.change("visible")
  t.mock.timers.tick(60_000)
  await setImmediate()
  assert.equal(h.calls.length, calls)
})

test("visibility wake during a native read is retained, coalesced and cleaned after exit", async (t) => {
  const document = visibilityDocument("hidden")
  const h = monitorHarness(t, { document })
  const controller = new AbortController()
  const output = []
  const pending = h.execUpdate("synthetic-update", (text) => output.push(text), 60_000, controller.signal, () => {})
  await setImmediate()
  document.change("visible")
  document.change("hidden")
  document.change("visible")
  await setImmediate()
  assert.equal(h.reads.length, 1, "wake must not start overlapping native jobs")
  reply(h.reads[0].child, "RUNNING", "same log")
  await setImmediate()
  assert.equal(h.reads.length, 2, "wake must survive the in-flight read")
  assert.equal(h.timers.size, 1, "only the active native read has a timer")
  reply(h.reads[1].child, "130", "same log")
  const result = await pending
  assert.equal(result.errno, 130)
  assert.deepEqual(output, ["same log\n"])
  assertMonitorClean(h, document, controller.signal)
  const calls = h.calls.length
  document.change("hidden")
  document.change("visible")
  h.reads[0].child.stdout.emit("data", "late log")
  h.reads[1].child.emit("exit", 0)
  t.mock.timers.tick(60_000)
  await setImmediate()
  assert.equal(h.calls.length, calls)
  assert.deepEqual(output, ["same log\n"])
})

test("startup abort retains the late directory callback within the original 15s bound", async (t) => {
  for (const mode of ["late-exit", "timeout-with-path", "timeout-without-path"]) {
    await t.test(mode, async (t) => {
      const document = visibilityDocument("hidden")
      const h = monitorHarness(t, { document, launch() {} })
      const controller = new AbortController()
      const output = []
      let started = false
      let result
      const pending = h.execUpdate("synthetic-update", (text) => output.push(text), 60_000, controller.signal, () => { started = true })
      void pending.then((value) => { result = value })
      await setImmediate()
      const { args, child } = h.calls[0]
      const dir = "/data/local/tmp/keybox-webui.lateLaunch"
      controller.abort()
      document.change("visible")
      await setImmediate()
      assert.equal(result, undefined, "disposal must still wait for the submitted native launch")
      assert.equal(h.host[args[3]], child, "native output must retain its named callback")
      assert.equal(h.timers.size, 1, "startup retains only its bounded exec timer")
      assert.equal(h.calls.length, 1, "disposal must not signal the detached update")
      t.mock.timers.tick(14_999)
      await setImmediate()
      assert.equal(result, undefined)
      if (mode === "late-exit") {
        reply(h.host[args[3]], dir)
      } else {
        if (mode === "timeout-with-path") h.host[args[3]].stdout.emit("data", dir)
        t.mock.timers.tick(1)
      }
      assert.equal((await pending).failure, mode === "late-exit" ? "bridge" : "timeout")
      assert.equal(started, false, "an aborted launch must not expose stop or begin monitoring")
      assert.equal(h.reads.length, 0)
      assert.deepEqual(output, [])
      if (mode === "timeout-without-path") {
        assert.equal(h.calls.length, 1, "an unknown directory cannot be removed")
      } else {
        assert.equal(h.calls.length, 2)
        assert.equal(h.calls[1].args[0], "(\nrm -rf '" + dir + "'\n)\n")
      }
      assertMonitorClean(h, document, controller.signal)
      const calls = h.calls.length
      reply(child, dir)
      document.change("visible")
      t.mock.timers.tick(60_000)
      await setImmediate()
      assert.equal(h.calls.length, calls)
    })
  }
})

test("abort releases an outstanding read immediately, including a queued visibility wake", async (t) => {
  const document = visibilityDocument("hidden")
  const h = monitorHarness(t, { document })
  const controller = new AbortController()
  const output = []
  const pending = h.execUpdate("synthetic-update", (text) => output.push(text), 60_000, controller.signal, () => {})
  await setImmediate()
  assert.equal(h.callbacks().length, 1)
  document.change("visible")
  const child = h.reads[0].child
  controller.abort()
  assert.equal((await pending).failure, "bridge")
  assertMonitorClean(h, document, controller.signal)
  const calls = h.calls.length
  reply(child, "0", "late log")
  document.change("visible")
  t.mock.timers.tick(60_000)
  await setImmediate()
  assert.equal(h.calls.length, calls)
  assert.deepEqual(output, [])
})

test("abort immediately releases a stop request or readiness retry and the hidden polling delay", async (t) => {
  for (const mode of ["request", "retry"]) {
    await t.test(mode, async (t) => {
      const document = visibilityDocument("hidden")
      let stopChild
      let attempts = 0
      const h = monitorHarness(t, {
        document,
        read: (child) => reply(child, "RUNNING", "log"),
        stop: (child) => {
          stopChild = child
          attempts++
          if (mode === "retry") reply(child, "STARTING")
        },
      })
      const controller = new AbortController()
      let stop
      const pending = h.execUpdate("synthetic-update", () => {}, 60_000, controller.signal, (fn) => { stop = fn })
      await setImmediate()
      const stopping = stop()
      await setImmediate()
      assert.equal(h.timers.size, 2, "stop and monitor each have one outstanding timer")
      controller.abort()
      assert.equal((await pending).failure, "bridge")
      assert.equal((await stopping).failure, "bridge")
      assertMonitorClean(h, document, controller.signal)
      const calls = h.calls.length
      reply(stopChild)
      document.change("visible")
      t.mock.timers.tick(60_000)
      await setImmediate()
      assert.equal(h.calls.length, calls)
      assert.equal(attempts, 1)
    })
  }
})

test("hidden polling respects the remaining deadline and releases its visibility listener", async (t) => {
  const document = visibilityDocument("hidden")
  const h = monitorHarness(t, { document, read: (child) => reply(child, "RUNNING", "partial") })
  const controller = new AbortController()
  const output = []
  const pending = h.execUpdate("synthetic-update", (text) => output.push(text), 1200, controller.signal, () => {})
  await setImmediate()
  t.mock.timers.tick(1199)
  await setImmediate()
  assert.equal(h.reads.length, 1)
  t.mock.timers.tick(1)
  const result = await pending
  assert.equal(result.failure, "timeout")
  assert.equal(result.stdout, "partial\n")
  assert.equal(h.reads.length, 2)
  assert.deepEqual(output, ["partial\n"])
  assertMonitorClean(h, document, controller.signal)
})

test("an in-flight read cannot extend the update deadline", async (t) => {
  const document = visibilityDocument()
  const h = monitorHarness(t, {
    document,
    read: (child, count) => { if (count === 1) reply(child, "RUNNING", "partial") },
  })
  const controller = new AbortController()
  const pending = h.execUpdate("synthetic-update", () => {}, 700, controller.signal, () => {})
  let result
  void pending.then((value) => { result = value })
  await setImmediate()
  t.mock.timers.tick(500)
  await setImmediate()
  assert.equal(h.reads.length, 2)
  t.mock.timers.tick(199)
  await setImmediate()
  assert.equal(result, undefined)
  document.change("visible")
  t.mock.timers.tick(1)
  assert.equal((await pending).failure, "timeout")
  assertMonitorClean(h, document, controller.signal)
})

test("monitor completion clears a pending stop readiness retry and prevents later stop commands", async (t) => {
  const document = visibilityDocument("hidden")
  const h = monitorHarness(t, {
    document,
    read: (child, count) => reply(child, count === 1 ? "RUNNING" : "130", "same log"),
    stop: (child) => reply(child, "STARTING"),
  })
  const controller = new AbortController()
  let stop
  const pending = h.execUpdate("synthetic-update", () => {}, 60_000, controller.signal, (fn) => { stop = fn })
  await setImmediate()
  const stopping = stop()
  await setImmediate()
  assert.equal(h.timers.size, 2, "poll and stop each have an independent delay")
  document.change("visible")
  assert.equal((await pending).errno, 130)
  assert.equal((await stopping).failure, "bridge")
  assertMonitorClean(h, document, controller.signal)
  const calls = h.calls.length
  assert.equal((await stop()).failure, "bridge")
  t.mock.timers.tick(60_000)
  await setImmediate()
  assert.equal(h.calls.length, calls)
})

test("completion preserves a late native stop acknowledgement for cancellation classification", async (t) => {
  const document = visibilityDocument("hidden")
  let stopChild
  const h = monitorHarness(t, {
    document,
    read: (child, count) => reply(child, count === 1 ? "RUNNING" : "130", "same log"),
    stop: (child) => { stopChild = child },
  })
  const controller = new AbortController()
  let stop
  let completed = false
  const pending = h.execUpdate("synthetic-update", () => {}, 60_000, controller.signal, (fn) => { stop = fn })
  void pending.then(() => { completed = true })
  await setImmediate()
  const stopping = stop()
  await setImmediate()
  document.change("visible")
  await setImmediate()
  assert.equal(h.reads.length, 2)
  assert.equal(completed, false, "the native stop result must survive update completion")
  assert.equal(h.callbacks().length, 1)
  reply(stopChild)
  assert.equal((await stopping).errno, 0)
  assert.equal((await pending).errno, 130)
  assertMonitorClean(h, document, controller.signal)
})

test("concurrent monitors keep output caches, wake requests and cleanup independent", async (t) => {
  const document = visibilityDocument("hidden")
  const h = monitorHarness(t, { document, read: (child) => reply(child, "RUNNING", "same log") })
  const first = new AbortController()
  const second = new AbortController()
  const outputs = [[], []]
  const one = h.execUpdate("synthetic-one", (text) => outputs[0].push(text), 60_000, first.signal, () => {})
  const two = h.execUpdate("synthetic-two", (text) => outputs[1].push(text), 60_000, second.signal, () => {})
  await setImmediate()
  assert.deepEqual(outputs, [["same log\n"], ["same log\n"]])
  assert.equal(document.listeners.size, 2)
  assert.equal(h.reads.length, 2)
  first.abort()
  assert.equal((await one).failure, "bridge")
  assert.equal(document.listeners.size, 1)
  assert.equal(getEventListeners(first.signal, "abort").length, 0)
  document.change("visible")
  await setImmediate()
  assert.equal(h.reads.length, 3)
  assert.match(h.reads.at(-1).command, /keybox-webui\.testing2/)
  t.mock.timers.tick(500)
  await setImmediate()
  assert.equal(h.reads.length, 4)
  assert.deepEqual(outputs, [["same log\n"], ["same log\n"]])
  second.abort()
  assert.equal((await two).failure, "bridge")
  assertMonitorClean(h, document, second.signal)
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
    // mksh keeps saved pipe descriptors in the background shell. Capture to
    // files so spawnSync waits for this job's shell exit without waiting for EOF.
    const paths = ["stdout", "stderr"].map((stream) => join(root, stream + h.calls.length))
    const fds = paths.map((path) => openSync(path, "w"))
    let result
    try {
      result = spawnSync("sh", [], {
        input: command.replaceAll("/data/local/tmp", temp),
        stdio: ["pipe", ...fds], timeout: 2000,
      })
    } finally {
      for (const fd of fds) closeSync(fd)
    }
    assert.equal(result.error, undefined, "native shell must return before completion")
    const [stdout, stderr] = paths.map((path) => readFileSync(path, "utf8"))
    for (const line of stdout.trimEnd().split("\n").filter(Boolean)) {
      child.stdout.emit("data", line.replaceAll(temp, "/data/local/tmp"))
    }
    if (stderr) child.stderr.emit("data", stderr.trimEnd())
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
