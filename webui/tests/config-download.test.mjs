import assert from "node:assert/strict"
import { test } from "node:test"
import { readFileSync, mkdtempSync, mkdirSync, readdirSync, statSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { spawnSync } from "node:child_process"
import { runInNewContext } from "node:vm"
import { webcrypto } from "node:crypto"
import ts from "typescript"

const compile = path => ts.transpileModule(readFileSync(new URL(path, import.meta.url), "utf8"), {
  compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.CommonJS },
}).outputText
const targets = {}
runInNewContext(compile("../src/lib/targets.ts"), { exports: targets, require: () => ({}) })
const source = compile("../src/lib/configDownload.ts")
const payload = extra => JSON.stringify({ format: "keybox-lite.targets", version: 1, ...extra })
function harness({ exec, native = true } = {}) {
  const exports = {}, downloads = [], urls = new Map(), revoked = []
  const document = {
    body: { append(anchor) { anchor.attached = true } },
    createElement() {
      return {
        click() { downloads.push({ name: this.download, blob: urls.get(this.href) }) },
        remove() { this.attached = false },
      }
    },
  }
  runInNewContext(source, {
    exports, require: name => name === "./targets" ? targets : { exec },
    window: native ? { ksu: { spawn() {} } } : {}, document,
    crypto: webcrypto, TextEncoder, Blob, setTimeout, btoa,
    URL: {
      createObjectURL(blob) { const id = "blob:" + urls.size; urls.set(id, blob); return id },
      revokeObjectURL(id) { urls.delete(id); revoked.push(id) },
    },
  })
  return { ...exports, downloads, urls, revoked }
}
function device(t, hook = () => {}) {
  const root = mkdtempSync(join(tmpdir(), "keybox config export "))
  mkdirSync(join(root, "tmp"))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const commands = []
  const map = value => value.replaceAll("/data/local/tmp", join(root, "tmp")).replaceAll("/sdcard/Download", join(root, "Download"))
  const restore = value => value.replaceAll(join(root, "tmp"), "/data/local/tmp").replaceAll(join(root, "Download"), "/sdcard/Download")
  const h = harness({
    async exec(command) {
      commands.push(command)
      const replacement = await hook(command)
      if (replacement && typeof replacement !== "string") return replacement
      const result = spawnSync("sh", ["-c", map(replacement || command)], { encoding: "utf8", timeout: 5000 })
      assert.equal(result.error, undefined)
      return { errno: result.status, stdout: restore(result.stdout), stderr: result.stderr }
    },
  })
  return { ...h, root, commands, map }
}

test("native configuration export preserves UTF-8 and shell literals across bounded chunks and cleans staging", async t => {
  const h = device(t)
  const contents = payload({ value: ("中文 ' \" ` $() \\ line\n").repeat(7000) })
  const result = await h.saveConfiguration("keybox-lite-targets.json", contents)
  assert.match(result.path, /^\/sdcard\/Download\/keybox-lite-targets-[0-9a-f]{24}\.json$/)
  assert.equal(readFileSync(h.map(result.path), "utf8"), contents)
  assert.ok(h.commands.filter(command => command.includes("base64 -d")).length > 2)
  assert.ok(h.commands.every(command => Buffer.byteLength(command) < 34000), "each native message stays below the selected bridge budget")
  assert.deepEqual(readdirSync(join(h.root, "tmp")), [])
  assert.deepEqual(readdirSync(join(h.root, "Download")), [result.path.split("/").at(-1)])
  assert.equal(statSync(h.map(result.path)).mode & 0o777, 0o600)
})

test("separate exports do not overwrite an earlier file", async t => {
  const h = device(t)
  const first = await h.saveConfiguration("targets.json", payload({ entry: 1 }))
  const second = await h.saveConfiguration("targets.json", payload({ entry: 2 }))
  assert.notEqual(first.path, second.path)
  assert.equal(JSON.parse(readFileSync(h.map(first.path), "utf8")).entry, 1)
  assert.equal(JSON.parse(readFileSync(h.map(second.path), "utf8")).entry, 2)
})

test("invalid filenames, oversized UTF-8 and non-target documents never reach the native bridge", async () => {
  let calls = 0
  const h = harness({ exec() { calls++; throw Error("must not write") } })
  for (const [name, contents] of [
    ["../targets.json", payload({})], ["bad.json;touch-pwned", payload({})],
    ["targets.xml", payload({})], ["targets.json", "not json"],
    ["targets.json", payload({ version: 2 })],
    ["targets.json", JSON.stringify({ format: "other", version: 1 })],
    ["targets.json", payload({ value: "中".repeat(400000) })],
  ]) await assert.rejects(h.saveConfiguration(name, contents), error => error.code === "invalid")
  const controller = new AbortController()
  controller.abort()
  await assert.rejects(h.saveConfiguration("targets.json", payload({}), controller.signal), error => error.code === "aborted")
  assert.equal(calls, 0)
})

test("failed upload and publication never publish partial JSON or expose native command errors", async t => {
  for (const phase of ["append", "publish"]) await t.test(phase, async t => {
    const h = device(t, command => {
      if (phase === "append" && command.includes("base64 -d")) return { errno: 1, stdout: "", stderr: "private-command-details" }
      if (phase === "publish" && command.includes("export_dir=")) return command.replace('mv "$stage"', 'false "$stage"')
    })
    await assert.rejects(h.saveConfiguration("targets.json", payload({})), error =>
      error.code === "write" && !error.message.includes("private-command-details"))
    assert.deepEqual(readdirSync(join(h.root, "tmp")), [])
    const download = join(h.root, "Download")
    assert.deepEqual(readdirSync(h.root).includes("Download") ? readdirSync(download) : [], [])
  })
})

test("cancellation after a native create or append waits for the current reply and removes its work", async t => {
  for (const phase of ["create", "append"]) await t.test(phase, async t => {
    const controller = new AbortController()
    const h = device(t, command => {
      if ((phase === "create" && command.startsWith("umask 077\nmkdir")) ||
          (phase === "append" && command.includes("base64 -d"))) controller.abort()
    })
    await assert.rejects(h.saveConfiguration("targets.json", payload({}), controller.signal), error => error.code === "aborted")
    assert.deepEqual(readdirSync(join(h.root, "tmp")), [])
    assert.equal(readdirSync(h.root).includes("Download"), false)
    assert.ok(h.commands.at(-1).startsWith("rm -rf "))
  })
})

test("ordinary browsers download the selected JSON and release their anchor and object URL", async () => {
  const h = harness({ native: false })
  const contents = payload({ targets: ["com.example"] })
  const result = await h.saveConfiguration("targets.json", contents)
  assert.equal(result.path, null)
  assert.equal(h.downloads.length, 1)
  assert.equal(h.downloads[0].name, "targets.json")
  assert.equal(await h.downloads[0].blob.text(), contents)
  assert.equal(h.urls.size, 0)
  assert.equal(h.revoked.length, 1)
})
