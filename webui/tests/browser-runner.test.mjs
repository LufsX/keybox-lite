import assert from "node:assert/strict"
import { test } from "node:test"
import { once } from "node:events"
import { createServer } from "node:net"
import { mkdtemp, mkdir, readFile, rm, symlink, writeFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join, dirname } from "node:path"
import { fileURLToPath } from "node:url"
import { spawn } from "node:child_process"

const root = dirname(dirname(fileURLToPath(import.meta.url)))
const wait = ms => new Promise(resolve => setTimeout(resolve, ms))
async function marker(path) {
  for (let count = 0; count < 150; count++) {
    try { return JSON.parse(await readFile(path, "utf8")) }
    catch (error) { if (error.code !== "ENOENT") throw error; await wait(50) }
  }
  throw Error("runner fixture did not start")
}
function launch(directory, output, extra = {}) {
  const child = spawn(process.execPath, ["tests/browser-runner.mjs"], {
    cwd: directory, env: { ...process.env, KEYBOX_RUNNER_RESULT: output, ...extra }, stdio: ["ignore", "pipe", "pipe"],
  })
  let log = ""
  child.stdout.on("data", data => { log += data })
  child.stderr.on("data", data => { log += data })
  const done = once(child, "close").then(([code, signal]) => ({ code, signal, log }))
  return { child, done }
}
async function portOpen(port) {
  const { createConnection } = await import("node:net")
  return new Promise(resolve => {
    const client = createConnection({ host: "127.0.0.1", port })
    client.once("connect", () => { client.destroy(); resolve(true) })
    client.once("error", () => resolve(false))
  })
}

test("browser runners use distinct ports concurrently and preserve failure and interruption exits", { timeout: 45000 }, async t => {
  const directory = await mkdtemp(join(tmpdir(), "keybox browser runner "))
  const running = []
  const existing = createServer()
  let defaultHeld = false
  try {
    existing.listen(5173, "127.0.0.1")
    await once(existing, "listening")
    defaultHeld = true
  } catch (error) {
    assert.equal(error.code, "EADDRINUSE")
  }
  t.after(async () => {
    for (const run of running) {
      if (run.child.exitCode === null && run.child.signalCode === null) run.child.kill("SIGTERM")
    }
    await Promise.all(running.map(run => run.done))
    if (defaultHeld) await new Promise(resolve => existing.close(resolve))
    await rm(directory, { recursive: true, force: true })
  })
  await mkdir(join(directory, "tests"))
  await writeFile(join(directory, "package.json"), '{"type":"module"}')
  await writeFile(join(directory, "vite.config.mjs"), "export default {}")
  await symlink(join(root, "node_modules"), join(directory, "node_modules"), "dir")
  // Use an isolated Vite config so parallel fixtures share no dependency cache.
  let runner = await readFile(join(root, "tests/browser-runner.mjs"), "utf8")
  runner = runner.replace('join(root, "vite.config.ts")', 'join(root, "vite.config.mjs")')
  await writeFile(join(directory, "tests/browser-runner.mjs"), runner)
  await writeFile(join(directory, "fake-playwright.mjs"), `export const chromium={executablePath:()=>process.execPath}`)
  await writeFile(join(directory, "tests/fixture.browser.mjs"), `
    import { writeFileSync } from "node:fs";
    const url=process.argv[2];
    if((await fetch(url+"/@vite/client")).status!==200) throw Error("Vite unavailable");
    writeFileSync(process.env.KEYBOX_RUNNER_RESULT,JSON.stringify({url,pid:process.pid}));
    if(process.env.KEYBOX_RUNNER_WAIT) setInterval(()=>{},1000);
    else process.exit(Number(process.env.KEYBOX_RUNNER_CODE||0));
  `)
  const module = join(directory, "fake-playwright.mjs")
  const firstPath = join(directory, "one.json"), secondPath = join(directory, "two.json")
  const first = launch(directory, firstPath, { PLAYWRIGHT_MODULE: module, KEYBOX_RUNNER_WAIT: "1" })
  const second = launch(directory, secondPath, { PLAYWRIGHT_MODULE: module, KEYBOX_RUNNER_WAIT: "1" })
  running.push(first, second)
  const [one, two] = await Promise.all([marker(firstPath), marker(secondPath)])
  const ports = [one, two].map(item => Number(new URL(item.url).port))
  assert.notEqual(ports[0], ports[1])
  assert.ok(ports.every(port => port !== 5173))
  assert.ok((await Promise.all(ports.map(portOpen))).every(Boolean))
  first.child.kill("SIGINT")
  second.child.kill("SIGTERM")
  assert.equal((await first.done).code, 130)
  assert.equal((await second.done).code, 143)
  assert.deepEqual(await Promise.all(ports.map(portOpen)), [false, false])
  if (defaultHeld) assert.equal(await portOpen(5173), true, "the runner cannot shut down another service")
  for (const expected of [0, 7]) {
    const path = join(directory, "exit-" + expected + ".json")
    const run = launch(directory, path, { PLAYWRIGHT_MODULE: module, KEYBOX_RUNNER_CODE: String(expected) })
    running.push(run)
    const result = await run.done
    assert.equal(result.code, expected, result.log)
    const data = await marker(path)
    assert.equal(await portOpen(Number(new URL(data.url).port)), false)
  }
})
