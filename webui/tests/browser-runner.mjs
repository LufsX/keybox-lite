import { access, readdir } from "node:fs/promises"
import { constants } from "node:fs"
import { spawn } from "node:child_process"
import { createServer as createPortProbe } from "node:net"
import { fileURLToPath } from "node:url"
import { basename, dirname, join } from "node:path"
import { createServer } from "vite"

const testDirectory = dirname(fileURLToPath(import.meta.url))
const root = dirname(testDirectory)
const timeout = 120_000
let server
let child
let killTimer
let interrupted = 0

function terminate(signal = "SIGTERM") {
  if (!child || child.exitCode !== null || child.signalCode !== null) return
  try {
    if (process.platform === "win32") child.kill(signal)
    else process.kill(-child.pid, signal)
  } catch (error) {
    if (error.code !== "ESRCH") throw error
  }
  if (!killTimer) {
    const active = child
    killTimer = setTimeout(() => {
      if (active.exitCode !== null || active.signalCode !== null) return
      try {
        if (process.platform === "win32") active.kill("SIGKILL")
        else process.kill(-active.pid, "SIGKILL")
      } catch (error) { if (error.code !== "ESRCH") console.error(error.message) }
    }, 2000)
    killTimer.unref()
  }
}
const onInterrupt = () => { interrupted = 130; terminate("SIGINT") }
const onTerminate = () => { interrupted = 143; terminate() }

// Vite 6 treats port 0 as its default port. Reserve an OS-assigned candidate
// first, then use strictPort and retry if another process claims the gap.
async function availablePort() {
  const probe = createPortProbe()
  return new Promise((resolve, reject) => {
    probe.once("error", reject)
    probe.listen(0, "127.0.0.1", () => {
      const address = probe.address()
      probe.close(error => error ? reject(error) : resolve(address.port))
    })
  })
}
async function startServer() {
  for (let attempt = 0; attempt < 3 && !interrupted; attempt++) {
    const port = await availablePort()
    server = await createServer({
      root, configFile: join(root, "vite.config.ts"), logLevel: "error",
      server: { host: "127.0.0.1", port, strictPort: true, open: false },
    })
    try { await server.listen(); return }
    catch (error) {
      await server.close()
      server = undefined
      if (!/Port [0-9]+ is already in use/.test(error.message) || attempt === 2) throw error
    }
  }
}

function run(file, url) {
  return new Promise((resolve, reject) => {
    let timedOut = false
    const active = spawn(process.execPath, [join(testDirectory, file), url], {
      cwd: root,
      stdio: "inherit",
      detached: process.platform !== "win32",
    })
    child = active
    const timer = setTimeout(() => { timedOut = true; terminate() }, timeout)
    timer.unref()
    const cleanup = () => {
      clearTimeout(timer)
      clearTimeout(killTimer)
      killTimer = undefined
      child = undefined
    }
    active.once("error", error => { cleanup(); reject(error) })
    active.once("close", (code, signal) => {
      cleanup()
      if (timedOut) reject(new Error(file + " exceeded " + timeout / 1000 + " seconds"))
      else resolve(interrupted || code || (signal ? 1 : 0))
    })
  })
}

process.on("SIGINT", onInterrupt)
process.on("SIGTERM", onTerminate)
try {
  const known = (await readdir(testDirectory)).filter(file => file.endsWith(".browser.mjs")).sort()
  const requested = process.argv.slice(2)
  const files = requested.length ? requested : known
  if (!files.length) throw new Error("No browser regression tests found")
  for (const file of files) {
    if (!known.includes(file)) throw new Error("Unknown browser test: " + file)
  }
  const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright")
  try { await access(chromium.executablePath(), constants.X_OK) }
  catch { throw new Error("Chromium is missing. Run npm run test:browser:install first.") }

  if (!interrupted) {
    await startServer()
    if (interrupted) process.exitCode = interrupted
    const address = server?.httpServer?.address()
    if (!address || typeof address === "string") throw new Error("Could not determine the browser test server address")
    const url = "http://127.0.0.1:" + address.port
    for (const file of files) {
      if (interrupted) break
      console.log("Browser regression: " + basename(file))
      const code = await run(file, url)
      if (code) { process.exitCode = code; break }
    }
    if (!process.exitCode && !interrupted) console.log("Browser regressions passed: " + files.length)
  }
} catch (error) {
  console.error(error instanceof Error ? error.message : String(error))
  process.exitCode = interrupted || 1
} finally {
  if (child) terminate()
  clearTimeout(killTimer)
  try { await server?.close() }
  catch (error) { console.error(error.message); process.exitCode ||= 1 }
  process.off("SIGINT", onInterrupt)
  process.off("SIGTERM", onTerminate)
  if (interrupted) process.exitCode = interrupted
}
