import assert from "node:assert/strict"
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright")
const browser = await chromium.launch({ headless: true })
const base = process.argv[2] || "http://127.0.0.1:5186"
const backupId = "keybox.xml.bak.1700000000"
const revision = "b".repeat(64)
const backend = {
  backend: "tricky-store", identity: "tricky-store", active: true,
  module_id: "tricky_store", module_dir: "/data/adb/modules/tricky_store",
  config_dir: "/data/adb/tricky_store", config_path: "/data/adb/tricky_store/target.txt", keybox_path: "/data/keybox.xml",
  install_mode: "atomic-replace", new_file_mode: "0644", reload: "backend-managed",
}
const preview = {
  backend, target: backend.keybox_path, revocation: "not_checked",
  id: backupId, revision, size: 16, modified: 1700000000, structure_valid: true, compatible: true,
  serials: ["90071992547409931234567890"],
  validity: { status: "valid", certificates: [{ serial: "90071992547409931234567890", not_before: 1, not_after: 2100000000, status: "valid" }] },
  integrity: { status: "valid", keys: [{ algorithm: "ec", private_key_valid: true, key_match: true, chain_valid: true, root: "google" }] },
}
const xml = Buffer.from('<Keybox>PRIVATE_RAW 测试 "\'$`\\\n</Keybox>')
const page = await browser.newPage({ viewport: { width: 390, height: 700 }, reducedMotion: "reduce" })
const errors = [], consoleOutput = []
page.on("pageerror", error => errors.push(error.message))
page.on("console", message => consoleOutput.push(message.text()))
try {
  const main = await (await fetch(base + "/src/main.ts")).text()
  const runtime = main.match(/import\s*\{\s*mount\s*\}\s*from\s*"([^"]+)"/)?.[1]
  assert.ok(runtime)
  await page.addInitScript(({ preview, backupId }) => {
    const state = window.native = {
      hold: "", pending: [], creates: [], discards: [], applies: [], sessions: {},
      fail: "", mismatch: false, invalid: false, compatible: true, listEmpty: false,
      target: preview.target, invalidBackup: false, changedRevision: false, integrity: undefined, validity: undefined,
    }
    const reply = (callback, value, code = 0, stderr = "") => {
      const target = window[callback]
      if (!target) return
      if (value !== undefined) target.stdout.emit("data", typeof value === "string" ? value : JSON.stringify(value))
      if (stderr) target.stderr.emit("data", stderr)
      target.emit("exit", code)
    }
    window.ksu = { spawn(command, _args, _options, callback) {
      const words = []
      let word = "", quote = "", started = false
      for (const char of command) {
        if (quote) {
          if (char === quote) quote = ""
          else word += char
        } else if (char === "'" || char === '"') {
          quote = char
          started = true
        } else if (/\s/.test(char)) {
          if (started) words.push(word)
          word = ""; started = false
        } else { word += char; started = true }
      }
      if (started) words.push(word)
      const index = words.indexOf("keybox-files")
      if (index < 0) throw new Error("Unexpected mock invocation")
      const [operation, id, offset] = words.slice(index + 2)
      const finish = () => {
        if (state.fail === operation) return reply(callback, "PRIVATE_RAW", 1, command + " PRIVATE_RAW")
        if (operation === "create") {
          const next = (state.creates.length + 1).toString(16).padStart(64, "0")
          state.creates.push(next)
          state.sessions[next] = { size: 0, chunks: [] }
          return reply(callback, { id: next })
        }
        if (operation === "append") {
          const base64 = /printf %s '([A-Za-z0-9+/=]+)'/.exec(command)?.[1]
          if (!base64) throw new Error("Invalid mock chunk")
          const bytes = Uint8Array.from(atob(base64), c => c.charCodeAt(0))
          const session = state.sessions[id]
          if (!session || session.size !== Number(offset)) throw new Error("Invalid mock offset")
          session.chunks.push(Array.from(bytes))
          session.size += bytes.length
          return reply(callback, { ok: true, size: session.size })
        }
        if (operation === "discard") {
          state.discards.push(id)
          delete state.sessions[id]
          return reply(callback, { ok: true })
        }
        const basename = state.target.slice(state.target.lastIndexOf("/") + 1)
        const value = { ...structuredClone(preview), compatible: state.compatible, structure_valid: !state.invalid,
          backend: { ...preview.backend, keybox_path: state.target }, target: state.target, id: basename + ".bak.1700000000",
          integrity: state.integrity === undefined ? preview.integrity : state.integrity,
          validity: state.validity === undefined ? preview.validity : state.validity }
        if (operation === "backups") return reply(callback, {
          backend: value.backend,
          target: state.target, backups: state.listEmpty ? [] : [value, {
            ...value, id: basename + ".bak.1600000000", modified: 1600000000,
            ...(state.invalidBackup ? { structure_valid: false, serials: [], validity: null, integrity: null } : {}),
          }],
        })
        if (operation === "inspect") return reply(callback, {
          ...value, id: state.mismatch ? "keybox.xml.bak.999" : id, size: state.sessions[id]?.size ?? value.size,
          revision: state.changedRevision ? "c".repeat(64) : value.revision,
          xml: "PRIVATE_RAW", error: "PRIVATE_RAW",
        })
      }
      if (state.hold === operation) state.pending.push({ operation, finish })
      else queueMicrotask(finish)
    } }
    window.releaseNative = () => {
      state.hold = ""
      for (const pending of state.pending.splice(0)) pending.finish()
    }
    window.applyKeybox = (operation, id, revision) => {
      state.applies.push({ operation, id, revision })
      return new Promise((resolve, reject) => {
        window.finishInstall = resolve
        window.failInstall = () => reject(new Error("PRIVATE_RAW command / XML"))
      })
    }
  }, { preview, backupId })
  await page.route("**/keybox-files-test", route => route.fulfill({
    contentType: "text/html",
    body: `<html><head><style>:root { --page-gutter: 12px; --content-padding: 8px; --line: #888; --panel: white; --text: #222; --muted: #555; --inset: #eee; --red: #c22; --accent: #36c; }</style></head>
    <body class="app-shell"><script type="module">
      import { mount, flushSync, unmount } from "${runtime}";
      import Harness from "/tests/KeyboxFilesHarness.svelte";
      import "/src/app.css";
      window.flushSync = flushSync;
      window.harness = mount(Harness, { target: document.body });
      window.destroyHarness = () => unmount(window.harness);
    </script></body></html>`,
  }))
  const configure = value => page.evaluate(value => window.flushSync(() => window.filesHarness.configure(value)), value)
  const state = value => page.evaluate(value => Object.assign(window.native, value), value)
  const wait = expression => page.waitForFunction(expression)
  const load = async () => {
    await page.goto(base + "/keybox-files-test")
    await page.getByRole("button", { name: "Import keybox", exact: true }).waitFor()
  }
  const openImport = () => page.getByRole("button", { name: "Import keybox", exact: true }).click()
  const select = async (buffer = xml, name = "a.xml") => {
    await page.locator('input[type="file"]').setInputFiles({ name, mimeType: "application/xml", buffer })
  }
  const confirm = () => page.getByRole("button", { name: "Install keybox", exact: true })
  const cancel = () => page.getByRole("button", { name: "Cancel", exact: true }).click()
  const color = locator => locator.evaluate(node => getComputedStyle(node).color)
  const colors = () => page.evaluate(() => {
    const probe = document.createElement("span")
    document.body.append(probe)
    const values = Object.fromEntries(["success", "red", "muted"].map(name => {
      probe.style.color = "var(--" + name + ")"
      return [name, getComputedStyle(probe).color]
    }))
    probe.remove()
    return values
  })
  const field = label => page.locator(".preview > div").filter({ has: page.getByText(label, { exact: true }) }).locator("dd")
  const checkPrivate = async () => {
    assert.equal((await page.locator("body").innerHTML()).includes("PRIVATE_RAW"), false)
    assert.equal(consoleOutput.some(line => line.includes("PRIVATE_RAW") || line.includes(xml.toString("base64"))), false)
  }

  // Import shows only metadata; closing the modal releases a completed session.
  await load()
  await openImport()
  assert.equal(await page.locator('input[type="file"]').getAttribute("accept"), "*/*", "Android XML files must remain selectable regardless of provider MIME type")
  await select()
  await confirm().waitFor()
  assert.equal(await confirm().isEnabled(), true)
  assert.ok((await page.getByRole("dialog").innerText()).includes("Installation includes an online revocation check."))
  assert.ok((await page.getByRole("dialog").innerText()).includes(preview.serials[0]))
  for (const dark of [false, true]) {
    await page.evaluate(dark => document.documentElement.classList.toggle("dark", dark), dark)
    const palette = await colors()
    for (const label of ["XML structure", "Local integrity", "Certificate validity", "Backend compatibility"]) {
      assert.equal(await color(field(label)), palette.success, label + " uses the theme success color")
    }
    assert.equal(await color(field("Algorithms / certificate chain").locator("span")), palette.success)
    assert.equal(await color(page.locator(".certificate-status")), palette.success)
    assert.equal(await color(page.getByText("Installation includes an online revocation check.", { exact: true })), palette.muted,
      "pending online revocation check remains neutral")
  }
  await checkPrivate()
  await cancel()
  await wait(() => window.native.discards.length === 1)
  assert.equal(await page.getByRole("dialog").count(), 0)

  // Preflight failures make no native create request.
  for (const [buffer, name, message] of [
    [Buffer.alloc(0), "a.xml", "Choose a nonempty XML file."],
    [Buffer.alloc(1024 * 1024 + 1), "a.xml", "The file exceeds 1 MiB."],
    [xml, "a.txt", "Choose an .xml file."],
  ]) {
    await load(); await openImport(); await select(buffer, name)
    assert.equal(await page.getByRole("alert").innerText(), message)
    assert.equal(await page.evaluate(() => window.native.creates.length), 0)
  }

  // Exact 1 MiB and binary bytes pass the real bridge and preserve chunk boundaries.
  await load(); await openImport()
  const binary = Buffer.from(Array.from({ length: 1024 * 1024 }, (_, n) => n % 256))
  await select(binary)
  await page.waitForFunction(() => document.querySelector("dialog .preview") || document.querySelector('[role="alert"]'))
  assert.equal(await page.getByRole("alert").count(), 0, "1 MiB upload must reach preview without an error")
  await confirm().waitFor()
  assert.equal(await page.evaluate(() => {
    const session = Object.values(window.native.sessions)[0]
    const bytes = session.chunks.flat()
    return bytes.length === 1024 * 1024 && bytes.every((byte, n) => byte === n % 256) &&
      session.chunks.every(chunk => chunk.length <= 24 * 1024)
  }), true)
  await cancel()
  await wait(() => window.native.discards.length === 1)

  // Invalid/incompatible previews cannot be confirmed.
  for (const [invalid, label] of [[{ compatible: false }, "Backend compatibility"], [{ invalid: true }, "XML structure"]]) {
    await load(); await state(invalid); await openImport(); await select()
    await confirm().waitFor()
    assert.equal(await confirm().isDisabled(), true)
    assert.equal(await page.evaluate(() => window.native.applies.length), 0)
    assert.equal(await color(field(label)), (await colors()).red)
  }

  // Unchecked local results stay neutral; failed crypto and dates retain the error color.
  for (const result of [null, { status: "unknown", keys: [] }]) {
    await load(); await state({ integrity: result, validity: null }); await openImport(); await select()
    await confirm().waitFor()
    const palette = await colors()
    for (const label of ["Local integrity", "Certificate validity"]) assert.equal(await color(field(label)), palette.muted)
    assert.equal(await color(field("Algorithms / certificate chain").locator("span")), palette.muted)
    assert.equal(await confirm().isDisabled(), true)
  }
  await load(); await state({
    integrity: { status: "invalid", keys: [{ ...preview.integrity.keys[0], chain_valid: false }] },
    validity: { status: "expired", certificates: [{ ...preview.validity.certificates[0], status: "expired" }] },
  }); await openImport(); await select()
  await confirm().waitFor()
  for (const label of ["Local integrity", "Certificate validity"]) assert.equal(await color(field(label)), (await colors()).red)
  assert.equal(await color(field("Algorithms / certificate chain").locator("span")), (await colors()).red)
  assert.equal(await color(page.locator(".certificate-status")), (await colors()).red)
  assert.equal(await confirm().isDisabled(), true)

  // Native errors containing an original command remain generic and release the session.
  await load(); await state({ fail: "append" }); await openImport(); await select()
  assert.equal(await page.getByRole("alert").innerText(), "Could not upload the file. Choose the file again.")
  await wait(() => window.native.discards.length === 1)
  await checkPrivate()

  // Repeated confirmations produce one parent request; parent disabled does not discard input.
  await load(); await openImport(); await select()
  await confirm().waitFor()
  await confirm().evaluate(button => { button.click(); button.click() })
  await wait(() => window.native.applies.length === 1)
  assert.equal(await page.getByRole("dialog").count(), 0)
  assert.equal(await page.evaluate(() => window.native.discards.length), 0)
  assert.ok(await page.getByRole("status", { name: "Installing…" }).isVisible())
  await page.getByRole("button", { name: "Saved log", exact: true }).click()
  assert.equal(await page.getByRole("log").innerText(), "Saved output")
  await page.getByRole("button", { name: "Current output", exact: true }).click()
  await page.evaluate(() => window.failInstall())
  await confirm().waitFor()
  assert.equal(await page.getByRole("alert").innerText(), "Installation failed. Check the output, then retry.")
  assert.equal(await confirm().isEnabled(), true)
  assert.equal(await page.evaluate(() => window.native.discards.length), 0)
  await checkPrivate()
  await confirm().click()
  await wait(() => window.native.applies.length === 2)
  await page.evaluate(() => window.finishInstall())
  await wait(() => window.native.discards.length === 1)
  assert.equal((await page.locator(".keybox-files").innerText()).includes("Keybox installed."), false)

  // Unmount/context changes during install wait for the parent before discarding.
  for (const [action, reject] of [["unmount", false], ["unmount", true], ["bin", false], ["moddir", true]]) {
    await load(); await openImport(); await select()
    await confirm().waitFor(); await confirm().click()
    await wait(() => window.native.applies.length === 1)
    await configure(action === "unmount" ? { mounted: false } : { [action]: "/new-context" })
    assert.equal(await page.evaluate(() => window.native.discards.length), 0)
    await page.evaluate(reject => reject ? window.failInstall() : window.finishInstall(), reject)
    await wait(() => window.native.discards.length === 1)
    assert.equal(await page.getByRole("dialog").count(), 0)
  }

  // A new instance follows global running state after navigation, while the old one cleans up.
  for (const operation of ["install", "restore"]) {
    await load(); await openImport(); await select()
    await confirm().waitFor(); await confirm().click()
    await wait(() => window.native.applies.length === 1)
    await configure({ mounted: false })
    await configure({ mounted: true, running: operation })
    const label = operation === "install" ? "Installing…" : "Restoring…"
    assert.ok(await page.getByRole("status", { name: label }).isVisible())
    assert.ok(await page.getByRole("button", { name: "Import keybox", exact: true }).isDisabled())
    await page.getByRole("button", { name: "Saved log", exact: true }).click()
    assert.equal(await page.getByRole("log").innerText(), "Saved output")
    assert.equal(await page.evaluate(() => window.native.discards.length), 0)
    await page.evaluate(() => window.finishInstall())
    await wait(() => window.native.discards.length === 1)
    assert.ok(await page.getByRole("button", { name: "Import keybox", exact: true }).isEnabled())
  }

  // running alone only changes presentation; it never cancels a prepared session.
  await load(); await openImport(); await select()
  await confirm().waitFor()
  await configure({ running: "restore" })
  assert.equal(await page.evaluate(() => window.native.discards.length), 0)
  assert.equal(await confirm().isDisabled(), true)
  await configure({ running: null })
  assert.equal(await confirm().isEnabled(), true)
  await cancel()
  await wait(() => window.native.discards.length === 1)

  // Completed previews are discarded on parent disable, context change, and destruction.
  for (const config of [{ disabled: true }, { bin: "/new-bin" }, { moddir: "/new-module" }, { mounted: false }]) {
    await load(); await openImport(); await select()
    await confirm().waitFor()
    await configure(config)
    await wait(() => window.native.discards.length === 1)
    assert.equal(await page.getByRole("dialog").count(), 0)
  }

  // Closing, disabling, changing context and unmounting all clean up a late create.
  for (const action of ["close", "disabled", "bin", "moddir", "unmount"]) {
    await load(); await state({ hold: "create" }); await openImport(); await select()
    await wait(() => window.native.pending.length === 1)
    if (action === "close") await cancel()
    else await configure(action === "unmount" ? { mounted: false } :
      action === "disabled" ? { disabled: true } : { [action]: "/new-context" })
    await page.evaluate(() => window.releaseNative())
    await wait(() => window.native.discards.length === 1)
    assert.equal(await page.getByRole("dialog").count(), 0)
    assert.equal(await page.evaluate(() => window.native.applies.length), 0)
  }

  // Aborted append is not cleaned up until the native write finishes.
  await load(); await state({ hold: "append" }); await openImport(); await select()
  await wait(() => window.native.pending.length === 1)
  await cancel()
  assert.equal(await page.evaluate(() => window.native.discards.length), 0)
  await page.evaluate(() => window.releaseNative())
  await wait(() => window.native.discards.length === 1)

  // A late arrayBuffer result cannot replace a newer selection.
  await load(); await openImport()
  await page.evaluate(() => {
    const read = File.prototype.arrayBuffer
    File.prototype.arrayBuffer = function() {
      if (this.name === "slow.xml") return new Promise(resolve => { window.releaseFileRead = () => read.call(this).then(resolve) })
      return read.call(this)
    }
  })
  await select(xml, "slow.xml")
  await wait(() => typeof window.releaseFileRead === "function")
  await select(Buffer.from("<Keybox>new</Keybox>"), "new.xml")
  await confirm().waitFor()
  await page.evaluate(() => window.releaseFileRead())
  await page.waitForTimeout(50)
  assert.equal(await page.evaluate(() => window.native.creates.length), 1)
  assert.equal(await confirm().isEnabled(), true)

  // Native file-picker cancellation also releases an existing preview.
  await page.locator('input[type="file"]').dispatchEvent("cancel")
  await wait(() => window.native.discards.length === 1)
  assert.equal(await confirm().count(), 0)

  // Double entry clicks issue one list read. A late closed list stays closed.
  await load(); await state({ hold: "backups" })
  await page.getByRole("button", { name: "Restore backup", exact: true }).evaluate(button => { button.click(); button.click() })
  await wait(() => window.native.pending.length === 1)
  await cancel()
  await page.evaluate(() => window.releaseNative())
  await page.waitForTimeout(50)
  assert.equal(await page.getByRole("dialog").count(), 0)

  // Backups retain server order and require a separate preview/confirmation step.
  await load()
  await page.getByRole("button", { name: "Restore backup", exact: true }).click()
  const backupButtons = page.locator(".backup")
  await backupButtons.first().waitFor()
  assert.equal(await backupButtons.count(), 2)
  for (const label of ["Valid", "Compatible"]) assert.equal(await color(backupButtons.first().getByText(label, { exact: true })), (await colors()).success)
  assert.equal(await page.getByRole("button", { name: "Restore this backup", exact: true }).count(), 0)
  await backupButtons.first().click()
  const restore = page.getByRole("button", { name: "Restore this backup", exact: true })
  await restore.waitFor()
  await restore.evaluate(button => { button.click(); button.click() })
  await wait(() => window.native.applies.length === 1)
  assert.deepEqual(await page.evaluate(() => window.native.applies[0]), { operation: "restore", id: backupId, revision })
  assert.equal(await page.getByRole("dialog").count(), 0)
  await page.getByRole("button", { name: "Saved log", exact: true }).click()
  await page.evaluate(() => window.failInstall())
  await restore.waitFor()
  assert.equal(await restore.isEnabled(), true)
  assert.equal(await page.getByRole("alert").innerText(), "Restore failed. Check the output, then retry.")
  await cancel()
  assert.equal(await page.evaluate(() => window.native.discards.length), 0)

  // A forged preview for another backup never reaches the parent callback.
  await load(); await state({ mismatch: true })
  await page.getByRole("button", { name: "Restore backup", exact: true }).click()
  await page.locator(".backup").first().click()
  assert.equal(await page.getByRole("alert").innerText(), "Could not read this preview.")
  assert.equal(await restore.count(), 0)
  assert.equal(await page.evaluate(() => window.native.applies.length), 0)

  // A changed file revision must be previewed from a fresh list, never silently installed.
  await load(); await state({ changedRevision: true })
  await page.getByRole("button", { name: "Restore backup", exact: true }).click()
  await page.locator(".backup").first().click()
  assert.equal(await page.getByRole("alert").innerText(), "Could not read this preview.")
  assert.equal(await restore.count(), 0)

  // Invalid backups stay listed alongside usable ones.
  await load(); await state({ invalidBackup: true })
  await page.getByRole("button", { name: "Restore backup", exact: true }).click()
  await page.locator(".backup").first().waitFor()
  assert.equal(await page.locator(".backup").count(), 2)
  assert.ok((await page.locator(".backup").nth(1).innerText()).includes("No certificates"))
  assert.equal(await color(page.locator(".backup").nth(1).getByText("Unknown", { exact: true })), (await colors()).muted)

  // TEES targets can have nested paths and non-ASCII/quoted/dot-prefixed basenames.
  for (const basename of [".keybox.xml", "my.bak.123.xml", "測試 keybox'$().xml"]) {
    await load()
    const target = "/data/tees/profiles/" + basename
    await state({ target })
    await page.getByRole("button", { name: "Restore backup", exact: true }).click()
    await page.locator(".backup").first().click()
    await restore.waitFor()
    assert.equal(await restore.isEnabled(), true)
    await restore.click()
    await wait(() => window.native.applies.length === 1)
    assert.deepEqual(await page.evaluate(() => window.native.applies[0]), { operation: "restore", id: basename + ".bak.1700000000", revision })
    await page.evaluate(() => window.finishInstall())
    await page.getByRole("button", { name: "Restore backup", exact: true }).waitFor()
  }

  // Empty lists and failed list reads have explicit states and can be retried.
  await load(); await state({ listEmpty: true })
  await page.getByRole("button", { name: "Restore backup", exact: true }).click()
  await page.getByText("No backups available.", { exact: true }).waitFor()
  await load(); await state({ fail: "backups" })
  await page.getByRole("button", { name: "Restore backup", exact: true }).click()
  assert.equal(await page.getByRole("alert").innerText(), "Could not read backups.")
  await state({ fail: "" })
  await page.getByRole("button", { name: "Retry", exact: true }).click()
  await page.locator(".backup").first().waitFor()

  // The dedicated dictionary resolves directly from all supported parent locales.
  await cancel()
  for (const [lang, label] of [["zh", "导入 keybox"], ["zh-TW", "匯入 keybox"], ["ja", "keybox を読み込む"], ["ko", "keybox 가져오기"], ["es", "Importar keybox"], ["ru", "Импорт keybox"]]) {
    await configure({ lang })
    assert.ok(await page.getByRole("button", { name: label, exact: true }).isVisible())
  }
  await checkPrivate()
  assert.deepEqual(errors, [])
  console.log("Keybox files browser regressions passed")
} finally {
  await browser.close()
}
