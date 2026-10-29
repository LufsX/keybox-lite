import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { test } from "node:test"
import ts from "typescript"

const source = path => readFileSync(new URL(path, import.meta.url), "utf8")
const dataURL = code => "data:text/javascript;base64," + Buffer.from(code).toString("base64")
const transpile = code => ts.transpileModule(code, {
  compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.ESNext },
}).outputText
const helper = source("../src/lib/keyboxFiles.ts")
const helperURL = dataURL(transpile(helper.replace('import { exec, type ExecResult } from "./bridge"', 'const exec = () => { throw new Error("unexpected real bridge") }')))
const {
  KeyboxFilesClient, parsePreview, canConfirm, shellQuote, isBackupId, MAX_KEYBOX_BYTES, KEYBOX_CHUNK_BYTES,
} = await import(helperURL)
const { keyboxFilesMessages } = await import(dataURL(transpile(source("../src/lib/keyboxFilesMessages.ts"))))
const id = "a".repeat(64)
const revision = "b".repeat(64)
const backupId = "keybox.xml.bak.1700000000"
const backend = {
  backend: "tricky-store", identity: "ts", active: true, module_id: "tricky_store", module_dir: "/data/adb/modules/tricky_store",
  config_dir: "/data/adb/tricky_store", config_path: "/data/adb/tricky_store/target.txt", keybox_path: "/data/keybox.xml",
  install_mode: "atomic-replace", new_file_mode: "0644", reload: "backend-managed",
}
const good = (changes = {}) => ({
  backend, target: backend.keybox_path, revocation: "not_checked",
  id, revision, size: 12, modified: 1700000000, structure_valid: true, compatible: true, serials: ["90071992547409931234567890"],
  validity: { status: "valid", certificates: [{ serial: "90071992547409931234567890", status: "valid", not_before: 1, not_after: 2100000000 }] },
  integrity: { status: "valid", keys: [{ algorithm: "ec", private_key_valid: true, key_match: true, chain_valid: true, root: "google" }] },
  ...changes,
})
const result = value => ({ errno: 0, stdout: JSON.stringify(value), stderr: "" })
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r }); return { promise, resolve } }
const file = bytes => ({ name: "测试'$().XML", size: bytes.length, arrayBuffer: async () => bytes.slice().buffer })
const command = (bin = "/module/keybox", moddir = "/module") => shellQuote(bin) + " keybox-files " + shellQuote(moddir)
function mockUpload(options = {}) {
  const calls = []
  const chunks = []
  let size = 0
  const execute = async cmd => {
    calls.push(cmd)
    if (cmd.endsWith(" create")) return options.create ? options.create() : result({ id })
    if (cmd.includes(" append ")) {
      const match = /^printf %s '([A-Za-z0-9+/=]+)' \| .* append '([a-f0-9]+)' ([0-9]+)$/.exec(cmd)
      assert.ok(match, "append must use a single quoted base64 chunk on stdin")
      assert.equal(match[2], id)
      assert.equal(Number(match[3]), size)
      const bytes = Buffer.from(match[1], "base64")
      assert.ok(bytes.length <= KEYBOX_CHUNK_BYTES)
      assert.ok(match[1].length <= 32768)
      chunks.push(bytes)
      size += bytes.length
      return options.append ? options.append(size) : result({ ok: true, size })
    }
    if (cmd.includes(" inspect ")) return options.inspect ? options.inspect() : result(good({ size }))
    if (cmd.includes(" discard ")) return result({ ok: true })
    throw new Error("unknown mock command")
  }
  return { calls, chunks, client: new KeyboxFilesClient("/module/keybox", "/module", execute) }
}

test("preview validates protocol and allowlists certificate data without raw XML/error fields", () => {
  const parsed = parsePreview(good({ error: "<private-key>secret</private-key>", xml: "secret" }), id)
  assert.equal(canConfirm(parsed), true)
  assert.equal(JSON.stringify(parsed).includes("secret"), false)
  assert.equal(parsed.serials[0], "90071992547409931234567890")
  const bad = [
    null, [], {}, good({ id: "../keybox.xml" }), good({ id: "keybox.xml.bak.1/2" }),
    good({ size: -1 }), good({ modified: NaN }), good({ revision: "bad" }), good({ serials: ["<XML>"] }),
    good({ compatible: 1 }), good({ structure_valid: "true" }), good({ validity: undefined }),
    good({ id: "a".repeat(32) }), good({ modified: -1 }), good({ backend: undefined }), good({ target: undefined }),
    good({ revocation: undefined }), good({ revocation: "not_revoked" }), good({ target: "/different.xml" }),
    good({ backend: { ...backend, active: "true" } }), good({ backend: { ...backend, module_dir: undefined } }),
    good({ id: "other.xml.bak.1" }),
    good({ validity: { status: "valid", certificates: [{ serial: "1", status: "valid", not_after: 2 }] } }),
    good({ integrity: { status: "valid", keys: [{ algorithm: "secret" }] } }),
  ]
  for (const value of bad) assert.throws(() => parsePreview(value), { code: "protocol" })
  assert.throws(() => parsePreview(good(), backupId), { code: "protocol" })
  assert.equal(parsePreview(good({ validity: { status: "valid", certificates: [{ serial: "1", not_before: -1, not_after: 2100000000, status: "valid" }] } })).validity.certificates[0].not_before, -1)
  assert.equal(canConfirm(parsePreview(good({ structure_valid: false, validity: null, integrity: null }))), false)
})

test("confirmation requires nonempty compatible XML, valid time and complete local integrity", () => {
  for (const changes of [
    { compatible: false }, { structure_valid: false }, { size: 0 }, { size: MAX_KEYBOX_BYTES + 1 }, { revision: "" },
    { serials: [] }, { validity: null }, { integrity: null },
    { validity: { status: "expired", certificates: [] } },
    { validity: { status: "valid", certificates: [{ status: "unknown" }] } },
    { integrity: { status: "valid", keys: [] } },
    { integrity: { status: "invalid", keys: [] } },
    { integrity: { status: "valid", keys: [{ private_key_valid: true, key_match: false, chain_valid: true }] } },
  ]) assert.equal(canConfirm(good(changes)), false)
})

test("backups preserve backend order and inspect is bound to the selected basename", async () => {
  const previews = [good({ id: backupId }), good({ id: "keybox.xml.bak.1600000000" })]
  const client = new KeyboxFilesClient("/bin with ' quote", "/module '$()", async cmd => {
    assert.ok(cmd.startsWith(command("/bin with ' quote", "/module '$()")))
    return cmd.endsWith("backups") ? result({
      backend, target: backend.keybox_path, backups: previews,
    }) : result(previews[1])
  })
  assert.deepEqual((await client.backups()).backups.map(p => p.id), previews.map(p => p.id))
  await assert.rejects(client.inspect(backupId, backend.keybox_path), { code: "protocol" })
  await assert.rejects(client.inspect("../keybox.xml.bak.1"), { code: "protocol" })
})

test("backup IDs bind the exact target basename, including TEES Unicode, spaces, dots and shell characters", async () => {
  for (const basename of [".keybox.xml", "my keybox.xml", "my.bak.123.xml", "測試'$().xml"]) {
    const target = "/data/tees/" + basename
    const id = basename + ".bak.1700000000"
    assert.equal(isBackupId(id, target), true)
    assert.equal(isBackupId(id), true)
    for (const bad of [id + "x", "../" + id, id + "/1", basename + ".bak.", "other.xml.bak.1"]) {
      assert.equal(isBackupId(bad, target), false)
    }
    const candidate = good({ id, target, backend: { ...backend, backend: "tee-simulator", keybox_path: target } })
    const calls = []
    const client = new KeyboxFilesClient("/bin", "/mod", async cmd => { calls.push(cmd); return result(candidate) })
    assert.equal((await client.inspect(id, target)).id, id)
    assert.ok(calls[0].endsWith("inspect " + shellQuote(id)))
  }
})

test("shell arguments reject NUL and backup identifiers reject paths and control characters", () => {
  assert.throws(() => shellQuote("a\u0000b"), { code: "protocol" })
  for (const id of [".bak.1", "x.bak.", "x.bak.1/2", "x\\y.bak.1", "x\nb.bak.1", "x\u0000b.bak.1"]) {
    assert.equal(isBackupId(id), false)
  }
})

test("invalid and oversized backup previews with null/empty details remain visible", async () => {
  const backups = [
    good({ id: backupId }),
    good({ id: "keybox.xml.bak.2", structure_valid: false, compatible: false, serials: [], validity: null, integrity: null }),
    good({ id: "keybox.xml.bak.1", size: MAX_KEYBOX_BYTES + 1, revision: "", structure_valid: false, compatible: false, serials: [], validity: null, integrity: null }),
    good({ id: "keybox.xml.bak.0", validity: { status: "unknown", certificates: [] }, integrity: { status: "invalid", keys: [] } }),
  ]
  const client = new KeyboxFilesClient("/b", "/m", async () => result({ backend, target: backend.keybox_path, backups }))
  const parsed = await client.backups()
  assert.equal(parsed.backups.length, 4)
  assert.equal(canConfirm(parsed.backups[0]), true)
  assert.ok(parsed.backups.slice(1).every(preview => !canConfirm(preview)))
})

test("malformed JSON, native errors, failed responses and duplicate backup IDs are rejected safely", async () => {
  for (const response of [
    { errno: 0, stdout: "{", stderr: "PRIVATE" },
    { errno: 1, stdout: "PRIVATE", stderr: "printf PRIVATE" },
    { errno: 0, stdout: "{}", stderr: "", failure: "bridge" },
    result({ backend: {}, target: "/x", backups: [] }),
    result({ backend, target: "/different.xml", backups: [] }),
    result({ backend: { ...backend, keybox_path: "/other.xml" }, target: "/other.xml", backups: [good({ id: backupId })] }),
    result({ backend, target: backend.keybox_path, backups: [good({ id: backupId }), good({ id: backupId })] }),
  ]) {
    const client = new KeyboxFilesClient("/b", "/m", async () => response)
    await assert.rejects(client.backups(), error => error.code === "protocol" && !error.message.includes("PRIVATE"))
  }
})

test("binary chunks retain exact bytes, Unicode and shell characters through 1 MiB", async () => {
  for (const length of [1, KEYBOX_CHUNK_BYTES - 1, KEYBOX_CHUNK_BYTES, KEYBOX_CHUNK_BYTES + 1, MAX_KEYBOX_BYTES]) {
    const bytes = Uint8Array.from({ length }, (_, n) => n % 256)
    const mock = mockUpload()
    const prepared = await mock.client.upload(file(bytes), new AbortController().signal)
    assert.deepEqual(Buffer.concat(mock.chunks), Buffer.from(bytes))
    assert.equal(prepared.preview.size, length)
    assert.equal(mock.calls.some(cmd => cmd.includes("echo")), false)
    await Promise.all([prepared.discard(), prepared.discard()])
    assert.equal(mock.calls.filter(cmd => cmd.includes(" discard ")).length, 1)
  }
  const bytes = new TextEncoder().encode('<Keybox>测试\n\r\t\'"$`\\</Keybox>')
  const mock = mockUpload()
  await (await mock.client.upload(file(bytes), new AbortController().signal)).discard()
  assert.deepEqual(Buffer.concat(mock.chunks), Buffer.from(bytes))
})

test("invalid size/extension and aborted file reads never create a session", async () => {
  for (const [sample, code] of [
    [{ ...file(new Uint8Array()), name: "a.xml" }, "empty"],
    [{ ...file(new Uint8Array(1)), size: MAX_KEYBOX_BYTES + 1 }, "tooLarge"],
    [{ ...file(new Uint8Array(1)), name: "a.txt" }, "extension"],
    [{ ...file(new Uint8Array(1)), arrayBuffer: async () => { throw new Error("PRIVATE") } }, "read"],
    [{ ...file(new Uint8Array(1)), size: 2 }, "read"],
  ]) {
    const mock = mockUpload()
    await assert.rejects(mock.client.upload(sample, new AbortController().signal), { code })
    assert.equal(mock.calls.length, 0)
  }
  const reading = deferred()
  const controller = new AbortController()
  const mock = mockUpload()
  const upload = mock.client.upload({ ...file(new Uint8Array(1)), arrayBuffer: () => reading.promise }, controller.signal)
  controller.abort()
  reading.resolve(new ArrayBuffer(1))
  await assert.rejects(upload, { code: "cancelled" })
  assert.equal(mock.calls.length, 0)
})

test("late create and in-flight append finish before aborted session cleanup", async () => {
  for (const phase of ["create", "append", "inspect"]) {
    const pending = deferred(), entered = deferred()
    const mock = mockUpload({ [phase]: () => { entered.resolve(); return pending.promise } })
    const controller = new AbortController()
    const upload = mock.client.upload(file(new Uint8Array(KEYBOX_CHUNK_BYTES + 1)), controller.signal)
    await entered.promise
    controller.abort()
    assert.equal(mock.calls.some(cmd => cmd.includes(" discard ")), false)
    pending.resolve(phase === "create" ? result({ id }) : phase === "append" ? result({ ok: true, size: KEYBOX_CHUNK_BYTES }) : result(good()))
    await assert.rejects(upload, { code: "cancelled" })
    assert.equal(mock.calls.filter(cmd => cmd.includes(" discard ")).length, 1)
    assert.ok(mock.calls.at(-1).includes(" discard "))
  }
})

test("bad chunk acknowledgements and unsafe bridge errors clean up without exposing contents", async () => {
  const logged = []
  const original = console.log, originalError = console.error
  console.log = (...args) => logged.push(args)
  console.error = (...args) => logged.push(args)
  try {
    for (const append of [
      async () => result({ ok: true, size: 0 }),
      async () => result({ ok: false, size: 1 }),
      async () => ({ errno: 1, stdout: "PRIVATE XML", stderr: "printf PRIVATE BASE64" }),
      async () => { throw new Error("PRIVATE XML and full command") },
    ]) {
      const mock = mockUpload({ append })
      await assert.rejects(mock.client.upload(file(new Uint8Array(1)), new AbortController().signal),
        error => error.code === "upload" && error.message === "upload")
      assert.equal(mock.calls.filter(cmd => cmd.includes(" discard ")).length, 1)
    }
  } finally { console.log = original; console.error = originalError }
  assert.equal(logged.length, 0)
})

test("all seven languages have complete dedicated messages with a local-only preview notice", () => {
  const keys = Object.keys(keyboxFilesMessages("en")).sort()
  for (const lang of ["en", "zh", "zh-TW", "ja", "ko", "es", "ru"]) {
    const messages = keyboxFilesMessages(lang)
    assert.deepEqual(Object.keys(messages).sort(), keys)
    assert.ok(Object.values(messages).every(value => typeof value === "string" && value.trim()))
    assert.notEqual(messages.recheck, "")
  }
})
