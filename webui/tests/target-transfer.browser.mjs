import assert from "node:assert/strict"
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright")
const browser = await chromium.launch({ headless: true })
const base = process.argv[2] || "http://127.0.0.1:5186"
const backend = { backend: "tricky-store", identity: "TS", active: true }
const initialDraft = {
  targets: [{ package_name: "com.zulu", mode: "hack" }, { package_name: "com.alpha", mode: "auto" }],
  default_policy: { os_patch: "202601", opaque_native: "keep" },
  per_app_policy: { "com.zulu": { vendor_patch: "prop", native: "keep" } },
  system_apps: ["com.system"], auto_add_new_apps: false,
}
const importedDraft = {
  targets: [{ package_name: "com.beta", mode: "generate" }, { package_name: "com.alpha", mode: "hack" }, { package_name: "com.missing", mode: "auto" }],
  default_policy: { os_patch: "202602" }, per_app_policy: { "com.beta": { boot_patch: "no" } },
  system_apps: [], auto_add_new_apps: true,
}
const document = draft => ({ format: "keybox-lite.targets", version: 1, backend: { backend: backend.backend, identity: backend.identity }, ...draft })
const file = value => ({ name: "untrusted-name.json", mimeType: "application/json", buffer: Buffer.from(typeof value === "string" ? value : JSON.stringify(value)) })
const errors = []
let page
try {
  const main = await (await fetch(base + "/src/main.ts")).text()
  const runtime = main.match(/import\s*\{\s*mount\s*\}\s*from\s*"([^"]+)"/)?.[1]
  assert.ok(runtime)
  page = await browser.newPage({ viewport: { width: 390, height: 720 }, reducedMotion: "reduce", acceptDownloads: true })
  page.on("pageerror", error => errors.push(error.message))
  await page.addInitScript(({ backend, initialDraft }) => {
    window.hostStatus = {
      supported: true, backend, config: { targets: initialDraft.targets, default_policy: initialDraft.default_policy, per_app_policy: initialDraft.per_app_policy },
      packages: ["com.alpha", "com.beta", "com.zulu", "com.system", ...Array.from({ length: 45 }, (_, i) => "com.app" + i)].map(package_name => ({
        package_name, installed: true, system: package_name === "com.system", selected: initialDraft.targets.some(t => t.package_name === package_name), mode: "auto",
      })),
      system_apps: initialDraft.system_apps, auto_add_new_apps: initialDraft.auto_add_new_apps,
      entry: { enabled: false, module_id: null, linked: false, has_own_webui: false }, revision: "revision-original",
    }
    window.targetWrites = []
    window.saveCalls = []
    window.clipboardTexts = []
    window.notices = []
    window.activeURLs = new Set()
    window.revokedURLs = []
    window.saveBehavior = "blob"
    window.clipboardFail = false
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: async text => {
      if (window.clipboardFail) throw Error("Clipboard denied")
      window.clipboardTexts.push(text)
    }, readText: async () => window.pasteText || "" } })
    const create = URL.createObjectURL.bind(URL), revoke = URL.revokeObjectURL.bind(URL)
    URL.createObjectURL = blob => { const url = create(blob); window.activeURLs.add(url); return url }
    URL.revokeObjectURL = url => { window.activeURLs.delete(url); window.revokedURLs.push(url); revoke(url) }
    const originalText = File.prototype.text
    File.prototype.text = function() {
      if (window.holdFile) {
        window.fileStarted = true
        const value = this
        return new Promise(resolve => { window.releaseFile = async () => resolve(await originalText.call(value)) })
      }
      window.fileReads = (window.fileReads || 0) + 1
      return originalText.call(this)
    }
    window.ksu = {
      toast: message => window.notices.push(message),
      getPackagesInfo: requested => JSON.stringify(JSON.parse(requested).map(packageName => ({ packageName, appLabel: packageName }))),
      spawn(command, _args, _options, callback) {
        setTimeout(() => {
          const child = window[callback]
          if (!child) return
          let stdout = "", stderr = "", errno = 0
          if (command.includes(" targets status")) stdout = JSON.stringify(window.hostStatus)
          else if (command.includes(" targets packages")) stdout = JSON.stringify(window.hostStatus.packages)
          else if (command.includes(" targets save")) {
            window.targetWrites.push(command)
            if (window.conflict) { errno = 1; stderr = "revision conflict; refresh status and retry" }
            else stdout = JSON.stringify({ ok: true })
          } else if (command.includes(" targets entry status")) stdout = JSON.stringify(window.hostStatus.entry)
          if (stdout) child.stdout.emit("data", stdout)
          if (stderr) child.stderr.emit("data", stderr)
          child.emit("exit", errno)
        }, 5)
      },
    }
  }, { backend, initialDraft })
  await page.route("**/src/lib/configDownload.ts*", route => route.fulfill({ contentType: "text/javascript", body: `
    export async function saveConfiguration(filename, contents, signal) {
      const call = { filename, contents, aborted: signal.aborted };
      window.saveCalls.push(call);
      signal.addEventListener("abort", () => { call.aborted = true }, { once: true });
      if (window.saveBehavior === "hold") return await new Promise(resolve => {
        window.releaseSave = () => resolve({ path: "/storage/emulated/0/Download/late.json" });
      });
      if (window.saveBehavior === "error") throw Error("Failed");
      if (window.saveBehavior === "path") return { path: "/storage/emulated/0/Download/" + filename };
      const url = URL.createObjectURL(new Blob([contents], { type: "application/json" }));
      const link = document.createElement("a");
      link.download = filename; link.href = url; link.click();
      URL.revokeObjectURL(url);
      return { path: null };
    }
  ` }))
  await page.route("**/transfer-test", route => route.fulfill({ contentType: "text/html", body: `
    <html><head><meta name="viewport" content="width=device-width,initial-scale=1"></head><body class="app-shell">
      <script type="module">
        import "/src/app.css";
        import { mount } from "${runtime}";
        import Harness from "/tests/TargetTransferHarness.svelte";
        mount(Harness, { target: document.body });
      </script>
    </body></html>
  ` }))
  await page.goto(base + "/transfer-test")
  await page.locator(".app-row-label").first().waitFor()
  const open = async mode => {
    await page.getByRole("button", { name: "More actions", exact: true }).click()
    await page.getByRole("menuitem", { name: mode + " configuration", exact: true }).click()
    await page.getByRole("dialog", { name: mode + " configuration", exact: true }).waitFor()
    assert.equal(await page.evaluate(() => !!document.activeElement?.closest("dialog")), true, "menu focus must not take focus from the dialog")
  }
  const close = async () => {
    await page.getByRole("dialog").getByRole("button", { name: /^(Cancel|Close)$/ }).last().click()
    await page.getByRole("dialog").waitFor({ state: "detached" })
  }
  const upload = value => page.getByLabel("Choose JSON file", { exact: true }).setInputFiles(file(value))
  const exportJSON = async () => {
    await open("Export")
    const text = await page.getByLabel("Configuration JSON", { exact: true }).inputValue()
    assert.equal(text, JSON.stringify(JSON.parse(text)), "exports contain no formatting whitespace")
    await close()
    return JSON.parse(text)
  }
  const original = await exportJSON()
  assert.deepEqual(original.targets, initialDraft.targets, "export preserves draft sequence, regardless of UI order")
  assert.deepEqual(original.backend, { backend: "tricky-store", identity: "TS" })
  assert.ok(!JSON.stringify(original).includes("opaque_native"))
  assert.equal(original.revision, undefined)
  await page.evaluate(() => localStorage.setItem("keybox-lite.target-list-preferences", JSON.stringify({ sort: "name", showSystem: false })))
  await page.evaluate(() => window.scrollTo(0, 500))
  const scroll = await page.evaluate(() => scrollY)
  const preferences = await page.evaluate(() => localStorage.getItem("keybox-lite.target-list-preferences"))
  await open("Import")
  const fileChooser = page.waitForEvent("filechooser")
  await page.locator(".file-picker").click()
  const chooser = await fileChooser
  assert.equal(chooser.isMultiple(), false, "file label keeps the native single-file picker")
  assert.equal(await page.getByLabel("Choose JSON file", { exact: true }).getAttribute("accept"), "*/*")
  await chooser.setFiles([])
  await page.evaluate(value => { window.pasteText = JSON.stringify(value) }, document(importedDraft))
  await page.getByRole("button", { name: "Paste JSON", exact: true }).click()
  await page.locator("[data-transfer-preview]").waitFor()
  assert.equal(await page.locator('[data-change="added"] dd').textContent(), "2")
  assert.equal(await page.locator('[data-change="removed"] dd').textContent(), "1")
  assert.equal(await page.locator('[data-change="modes"] dd').textContent(), "1")
  assert.equal(await page.locator("[data-missing]").textContent(), "com.missing")
  assert.equal(await page.locator("[data-transfer-preview]").textContent().then(text => text.includes("tricky-store")), false)
  await close()
  assert.deepEqual(await exportJSON(), original)
  assert.equal(await page.evaluate(() => scrollY), scroll)
  assert.equal(await page.evaluate(() => localStorage.getItem("keybox-lite.target-list-preferences")), preferences)
  assert.equal(await page.evaluate(() => window.targetWrites.length), 0)
  await open("Import")
  await upload(document(importedDraft))
  await page.getByRole("button", { name: "Load draft", exact: true }).click()
  await page.getByRole("dialog").waitFor({ state: "detached" })
  assert.equal(await page.evaluate(() => window.targetWrites.length), 0, "confirmation must only update the draft")
  assert.equal(await page.evaluate(() => { const event = new Event("beforeunload", { cancelable: true }); dispatchEvent(event); return event.defaultPrevented }), true)
  await open("Export")
  assert.match(await page.getByRole("dialog").textContent(), /unsaved draft/)
  const importedExport = JSON.parse(await page.getByLabel("Configuration JSON", { exact: true }).inputValue())
  assert.deepEqual(importedExport.targets, importedDraft.targets)
  await page.getByRole("button", { name: "Copy JSON", exact: true }).click()
  await page.waitForFunction(() => window.notices.some(text => /copied/i.test(text)))
  assert.deepEqual(JSON.parse(await page.evaluate(() => window.clipboardTexts.at(-1))), importedExport)
  await page.evaluate(() => { window.clipboardFail = true })
  await page.getByRole("button", { name: "Copy JSON", exact: true }).click()
  assert.match(await page.getByRole("alert").textContent(), /copy it manually/)
  assert.equal(await page.getByLabel("Configuration JSON", { exact: true }).evaluate(node => node.selectionEnd - node.selectionStart), JSON.stringify(importedExport).length)
  await page.getByRole("button", { name: "Select text", exact: true }).click()
  const downloadEvent = page.waitForEvent("download")
  await page.getByRole("button", { name: "Download JSON", exact: true }).click()
  const download = await downloadEvent
  assert.match(download.suggestedFilename(), /^keybox-lite-targets-[\dTZ-]+\.json$/)
  assert.equal(await page.evaluate(() => window.activeURLs.size), 0)
  assert.ok(await page.evaluate(() => window.revokedURLs.length > 0))
  const saved = await page.evaluate(() => window.saveCalls.at(-1))
  assert.deepEqual(JSON.parse(saved.contents), importedExport)
  assert.equal(saved.filename, download.suggestedFilename())
  await page.evaluate(() => { window.saveBehavior = "path" })
  await page.getByRole("button", { name: "Download JSON", exact: true }).click()
  await page.waitForFunction(() => window.notices.some(text => text.includes("/storage/emulated/0/Download/keybox-lite-targets-")))
  await page.evaluate(() => { window.saveBehavior = "error" })
  await page.getByRole("button", { name: "Download JSON", exact: true }).click()
  assert.match(await page.getByRole("alert").textContent(), /Could not save/)
  await close()
  await page.evaluate(() => { window.conflict = true })
  await page.getByRole("button", { name: "Save targets", exact: true }).click()
  await page.getByText("revision conflict; refresh status and retry", { exact: true }).waitFor()
  assert.deepEqual((await exportJSON()).targets, importedDraft.targets)
  assert.equal(await page.evaluate(() => window.targetWrites.length), 1)
  assert.match(await page.evaluate(() => window.targetWrites[0]), /revision-original/)
  // Importing an export provides a roundtrip preview with no effective changes.
  await open("Import")
  await upload(importedExport)
  await page.getByText("No changes to the current draft.", { exact: true }).waitFor()
  await close()
  // A file larger than the limit must never invoke File.text().
  await open("Import")
  const reads = await page.evaluate(() => window.fileReads)
  await upload(" ".repeat(1048577))
  await page.getByText("The JSON exceeds 1 MiB.", { exact: true }).waitFor()
  assert.equal(await page.evaluate(() => window.fileReads), reads)
  await upload("{")
  await page.getByText("Invalid JSON.", { exact: true }).waitFor()
  await upload({ ...document(importedDraft), version: 2 })
  await page.getByText("Unsupported configuration version.", { exact: true }).waitFor()
  await close()
  // Late reads cannot populate a closed or newly opened dialog, even after context changes.
  for (const action of ["close", "bin", "disabled", "leave", "unmount"]) {
    await open("Import")
    await page.evaluate(() => { window.holdFile = true; window.fileStarted = false })
    await upload(document({ ...importedDraft, targets: [{ package_name: "com.late", mode: "hack" }] }))
    await page.waitForFunction(() => window.fileStarted)
    if (action === "close") await close()
    else if (action === "bin") await page.evaluate(() => window.transferTest.context("/other/bin/keybox"))
    else if (action === "disabled") await page.evaluate(() => window.transferTest.disable(true))
    else if (action === "leave") {
      page.once("dialog", dialog => dialog.accept())
      assert.equal(await page.evaluate(() => window.transferTest.canLeave()), true)
    } else await page.evaluate(() => window.transferTest.unmount())
    await page.getByRole("dialog").waitFor({ state: "detached" })
    await page.evaluate(async () => { window.holdFile = false; await window.releaseFile() })
    if (action === "disabled") await page.evaluate(() => window.transferTest.disable(false))
    if (action !== "unmount") {
      await open("Export")
      assert.ok(!await page.getByLabel("Configuration JSON", { exact: true }).inputValue().then(text => text.includes("com.late")))
      await close()
    }
  }
  // Fresh manager: compatibility, pending-save cancellation and narrow layouts.
  await page.reload()
  await page.locator(".app-row-label").first().waitFor()
  await page.evaluate(() => {
    window.hostStatus.backend = { backend: "oh-my-keymint", identity: "OMK", active: true }
    window.hostStatus.config = { targets: [{ package_name: "com.alpha", mode: "auto" }], default_policy: {}, per_app_policy: {} }
    window.transferTest.context("/omk/bin")
  })
  await page.getByText("OhMyKeymint", { exact: true }).first().waitFor()
  await open("Import")
  const cross = structuredClone(importedDraft)
  cross.targets.push({ package_name: "uid:10001", mode: "hack" }, { package_name: "com.work@10", mode: "auto" })
  await upload(document(cross))
  await page.locator('[data-warning="modes"]').waitFor()
  assert.equal(await page.locator('[data-warning="identifiers"]').count(), 0)
  assert.match(await page.locator('[data-warning="modes"]').textContent(), /com.beta.*com.alpha.*uid:10001/)
  assert.ok(await page.locator('[data-warning="policies"]').count())
  for (const width of [320, 390, 640]) {
    await page.setViewportSize({ width, height: 720 })
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), JSON.stringify(await page.evaluate(() => ({ width: innerWidth, scroll: document.documentElement.scrollWidth, offenders: [...document.querySelectorAll("*")].filter(node => node.getBoundingClientRect().right > innerWidth).map(node => [node.tagName, node.className, node.getBoundingClientRect().right, node.textContent.slice(0, 80)]).slice(0, 12) }))))
    const box = await page.getByRole("dialog").boundingBox()
    assert.ok(box.x >= 0 && box.x + box.width <= width)
  }
  await page.getByRole("button", { name: "Load draft", exact: true }).click()
  await page.getByRole("dialog").waitFor({ state: "detached" })
  const converted = await exportJSON()
  assert.deepEqual(converted.targets.map(t => t.mode), ["auto", "auto", "auto", "auto", "auto"])
  assert.deepEqual(converted.targets.slice(-2).map(t => t.package_name), ["uid:10001", "com.work@10"])
  assert.deepEqual(converted.default_policy, {})
  assert.deepEqual(converted.system_apps, [])
  assert.equal(converted.auto_add_new_apps, true)
  // Both missing apps and compatibility names stay virtualized at the maximum list size.
  const large = {
    targets: Array.from({ length: 10000 }, (_, index) => ({
      package_name: "com.large" + String(index).padStart(5, "0"), mode: "hack",
    })),
    default_policy: {}, per_app_policy: {}, system_apps: [], auto_add_new_apps: false,
  }
  assert.ok(file(document(large)).buffer.byteLength <= 1048576)
  await open("Import")
  await upload(document(large))
  await page.locator("[data-transfer-preview]").waitFor()
  const missingNames = page.locator("[data-missing] [data-transfer-names]")
  const warningNames = page.locator('[data-warning="modes"] [data-transfer-names]')
  await missingNames.waitFor()
  await warningNames.waitFor()
  assert.equal(await page.locator("[data-transfer-names]").count(), 2)
  assert.ok(await page.getByRole("dialog").locator("*").count() < 350, "maximum-size preview has bounded DOM")
  assert.ok((await page.getByRole("dialog").textContent()).length < 5000, "warnings do not join every name into a giant text node")
  for (const names of [warningNames, missingNames]) {
    await names.scrollIntoViewIfNeeded()
    await names.getByText("com.large00000", { exact: true }).waitFor()
    assert.ok(await names.getByRole("listitem").count() < 30)
    assert.equal(await names.getByRole("listitem").first().getAttribute("aria-setsize"), "10000")
    assert.ok(await names.evaluate(node => node.clientHeight <= 144), "long lists keep a compact scroll region")
    await names.evaluate(node => { node.scrollTop = node.scrollHeight })
    await names.getByText("com.large09999", { exact: true }).waitFor()
    assert.ok(await names.getByRole("listitem").count() < 30, "scrolling does not accumulate rows")
    assert.equal(await names.getByRole("listitem").last().getAttribute("aria-posinset"), "10000")
    await names.evaluate(node => { node.scrollTop = Math.floor(node.scrollHeight / 2) })
    await names.locator('[data-index="5000"]').waitFor()
    await names.focus()
    await page.keyboard.press("Home")
    // Home is handled by the browser's scroll region, not the underlying menu.
    await names.evaluate(node => { node.scrollTop = 0 })
    await names.getByText("com.large00000", { exact: true }).waitFor()
  }
  // A new file restores the small-list layout without retaining the large virtual lists.
  await upload(document(cross))
  await page.waitForFunction(() => !document.querySelector("[data-transfer-names]"))
  assert.equal(await page.locator("[data-missing] li").count(), 2)
  await close()
  assert.deepEqual((await exportJSON()).targets, converted.targets, "large preview cancellation leaves the draft intact")
  await open("Export")
  await page.evaluate(() => { window.saveBehavior = "hold" })
  await page.getByRole("button", { name: "Download JSON", exact: true }).click()
  await page.waitForFunction(() => window.releaseSave)
  await close()
  assert.equal(await page.evaluate(() => window.saveCalls.at(-1).aborted), true)
  await page.evaluate(() => window.releaseSave())
  await open("Export")
  assert.equal(await page.locator("[data-saved-path]").count(), 0)
  await close()
  assert.deepEqual(errors, [])
  console.log("Target transfer browser regressions passed")
} finally {
  await browser.close()
}
