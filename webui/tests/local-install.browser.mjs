import assert from "node:assert/strict"
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright")
const browser = await chromium.launch({ headless: true })
const base = process.argv[2] || "http://127.0.0.1:5186"
try {
  for (const scenario of ["navigate", "reread-failure", "retry", "dispose", "monitor-failure"]) {
    const page = await browser.newPage({ viewport: { width: 390, height: 844 }, locale: "en" })
    const errors = []
    page.on("pageerror", error => errors.push(error.message))
    await page.addInitScript(scenario => {
      localStorage.setItem("keybox-lite-language", "en")
      const id = "a".repeat(64), revision = "b".repeat(64), backup = "keybox.xml.bak.123456"
      const backend = {
        backend: "tricky-store", identity: "TS", active: true, name: "Tricky Store",
        module_id: "tricky_store", module_dir: "/data/adb/modules/tricky_store",
        config_dir: "/data/adb/tricky_store", config_path: "/data/adb/tricky_store/config.ini",
        keybox_path: "/data/adb/tricky_store/keybox.xml", install_mode: "atomic",
        new_file_mode: "0644", reload: "none",
      }
      const makePreview = selected => ({
        id: selected, size: 100, modified: 1700000000, revision, structure_valid: true, compatible: true,
        serials: ["123"], backend, target: backend.keybox_path, revocation: "not_checked",
        validity: { status: "valid", certificates: [{ serial: "123", not_before: 1, not_after: 4102444800, status: "valid" }] },
        integrity: { status: "valid", keys: [{ algorithm: "ec", private_key_valid: true, key_match: true, chain_valid: true, root: "google" }] },
      })
      window.jobs = []
      window.discards = []
      window.toasts = []
      window.localDone = false
      window.localExit = scenario === "retry" ? 1 : 0
      window.infoReads = 0
      window.monitorFailure = false
      window.ksu = {
        enableEdgeToEdge() {},
        toast(text) { window.toasts.push(text) },
        getPackagesInfo() { return "[]" },
        spawn(command, _args, _options, callback) {
          let output = "", code = 0
          if (command.includes("mktemp -d /data/local/tmp/keybox-webui")) {
            window.jobs.push(command)
            window.localDone = false
            output = "/data/local/tmp/keybox-webui.localtest"
          } else if (command.includes('tail -c 80000 "$dir/log"')) {
            if (window.monitorFailure) code = 1
            output = (window.localDone ? String(window.localExit) : "RUNNING") + "\n" +
              (window.localDone ? (window.localExit ? "[FAIL] strict verification failed" : "[DONE] Keybox installed") : "[CHECK] Strict keybox verification")
          } else if (command.includes(" keybox-files ")) {
            if (command.includes(" backups")) output = JSON.stringify({ backend, target: backend.keybox_path, backups: [makePreview(backup)] })
            else if (command.includes(" create")) output = JSON.stringify({ id })
            else if (command.includes(" append ")) {
              const offset = Number(command.match(/ append '[a-f0-9]+' ([0-9]+)/)?.[1])
              const chunk = command.match(/printf %s '([A-Za-z0-9+/=]+)'/)?.[1] || ""
              output = JSON.stringify({ ok: true, size: offset + atob(chunk).length })
            } else if (command.includes(" inspect ")) output = JSON.stringify(makePreview(command.includes(backup) ? backup : id))
            else if (command.includes(" discard ")) { window.discards.push(id); output = JSON.stringify({ ok: true }) }
            else code = 2
          } else if (command.includes("for d in")) output = "/data/adb/modules/keybox-lite"
          else if (command.includes(" targets status")) output = JSON.stringify({
            supported: true, backend, config: { targets: [], default_policy: {}, per_app_policy: {} },
            packages: [], system_apps: [], auto_add_new_apps: false,
            entry: { enabled: false, module_id: "tricky_store", linked: false, has_own_webui: false }, revision: "r1",
          })
          else if (command.includes(" info")) {
            window.infoReads++
            if (scenario === "reread-failure" && window.jobs.length && window.localDone) code = 1
            else output = JSON.stringify({
              path: backend.keybox_path, exists: true, size: window.jobs.length && window.localDone ? 20 : 10,
              modified: window.jobs.length && window.localDone ? 2 : 1,
              structure_valid: true, serials: ["123"], backend,
            })
          } else if (command.includes("settings.sh")) output = "SCHEDULE=off\nLAST_RUN=0\nSOURCE=auto\nSKIP_REVOCATION=0"
          else if (command.includes("module.prop")) output = "version=2.0.0\n---\n"
          else if (command.includes("keybox-update.log")) output = "saved operation log"
          setTimeout(() => {
            const child = window[callback]
            if (!child) return
            for (const line of output.split("\n")) child.stdout.emit("data", line)
            if (code) child.stderr.emit("data", "synthetic operation failure")
            child.emit("exit", code)
          }, 10)
        },
      }
    }, scenario)
    if (scenario === "dispose") await page.route(/\/src\/main\.ts(?:\?.*)?$/, async route => {
      const response = await route.fetch()
      let source = await response.text()
      const runtime = source.match(/import\s*\{\s*mount\s*\}\s*from\s*"([^"]+)"/)?.[1]
      assert.ok(runtime)
      source = source.replace(/\bmount\(App,/, "window.mountedApp = mount(App,")
      source += '\nimport { unmount as testUnmount } from ' + JSON.stringify(runtime) +
        '; window.disposeApp = () => testUnmount(window.mountedApp);'
      await route.fulfill({ response, body: source })
    })
    await page.goto(base + "/")
    const importButton = page.getByRole("button", { name: "Import keybox", exact: true })
    const saved = page.getByRole("button", { name: "Update log", exact: true })
    const current = page.getByRole("button", { name: "Current output", exact: true })
    await importButton.waitFor()
    await page.waitForFunction(() => !document.querySelector('button[aria-label="Refresh"][disabled]'))
    if (scenario === "navigate") {
      const homeURL = page.url()
      await importButton.click()
      await page.getByRole("dialog").waitFor()
      await page.evaluate(() => history.back())
      await page.getByRole("dialog").waitFor({ state: "detached" })
      assert.equal(page.url(), homeURL, "Back closes import before leaving home")
      await page.locator('a[href="#/targets"]').click()
      await page.getByRole("searchbox").waitFor()
      const targetsURL = page.url()
      await page.getByRole("button", { name: "More actions", exact: true }).click()
      await page.getByRole("menuitem", { name: "Import configuration", exact: true }).click()
      await page.getByRole("dialog").waitFor()
      await page.evaluate(() => history.back())
      await page.getByRole("dialog").waitFor({ state: "detached" })
      assert.equal(page.url(), targetsURL, "Back closes target dialog before changing route")
      await page.evaluate(() => history.back())
      await importButton.waitFor()
      assert.equal(page.url(), homeURL)
    }
    await saved.click()
    await page.getByText("saved operation log", { exact: true }).waitFor()
    const restore = scenario === "reread-failure"
    if (restore) {
      await page.getByRole("button", { name: "Restore backup", exact: true }).click()
      await page.locator(".backup").click()
    } else {
      await importButton.click()
      await page.locator('input[type="file"]').setInputFiles({
        name: "synthetic.xml", mimeType: "application/xml", buffer: Buffer.from("synthetic local upload fixture"),
      })
    }
    const commitName = restore ? "Restore this backup" : "Install keybox"
    await page.getByRole("button", { name: commitName, exact: true }).click()
    await page.waitForFunction(() => window.jobs.length === 1)
    await page.waitForFunction(() => !document.querySelector("dialog[open]"))
    assert.equal(await saved.isEnabled(), true)
    assert.equal(await current.isEnabled(), true)
    assert.equal(await current.getAttribute("aria-pressed"), "true", "starting a local operation selects its live output")
    await saved.click()
    await page.getByText("saved operation log", { exact: true }).waitFor()
    assert.equal(await saved.getAttribute("aria-pressed"), "true")
    assert.equal(await page.getByRole("button", { name: "Update keybox", exact: true }).isDisabled(), true)
    if (scenario === "navigate") {
      await page.locator('a[href="#/targets"]').click()
      await page.getByRole("searchbox").waitFor()
      await page.getByRole("link", { name: "Back to home", exact: true }).click()
      await importButton.waitFor()
      await page.locator(".keybox-files .busy-status").waitFor()
      assert.equal(await importButton.isDisabled(), true, "an in-progress install survives remounting its controls")
      await saved.click()
    }
    if (scenario === "monitor-failure") {
      await page.evaluate(() => { window.monitorFailure = true })
      await page.getByRole("button", { name: commitName, exact: true }).waitFor()
      await page.getByText("Could not read the operation status. It may still be running; refresh before retrying.", { exact: true }).first().waitFor()
      await page.getByRole("dialog").getByRole("button", { name: "Cancel", exact: true }).click()
      await page.waitForFunction(() => !document.querySelector("dialog[open]"))
      assert.equal(await page.evaluate(() => window.discards.length), 0, "closing after an uncertain native result cannot cancel the continuing job")
      assert.deepEqual(errors, [])
      console.log("PASS: local keybox operation — failed observation preserves detached input and reports unknown status")
      await page.close()
      continue
    }
    if (scenario === "dispose") {
      await page.evaluate(() => window.disposeApp())
      await page.waitForTimeout(150)
      assert.equal(await page.evaluate(() => window.discards.length), 0, "disposal must not delete input still owned by a detached native install")
      assert.equal(await page.evaluate(() => Object.keys(window).filter(key => key.startsWith("keybox_spawn_")).length), 0,
        "disposal releases native monitor callbacks even when the job continues")
      assert.deepEqual(errors, [])
      console.log("PASS: local keybox operation — disposal keeps detached input and releases monitoring")
      await page.close()
      continue
    }
    await page.evaluate(() => { window.localDone = true })
    if (scenario === "retry") {
      await page.getByRole("button", { name: commitName, exact: true }).waitFor()
      assert.equal(await page.evaluate(() => window.discards.length), 0, "verification failures keep the sealed input for retry")
      await page.evaluate(() => { window.localExit = 0 })
      await page.getByRole("button", { name: commitName, exact: true }).click()
      await page.waitForFunction(() => window.jobs.length === 2)
      await saved.click()
      await page.evaluate(() => { window.localDone = true })
    }
    await page.waitForFunction(() => window.toasts.some(text => /installed|restored/i.test(text)))
    await page.waitForFunction(() => !document.querySelector(".keybox-files button")?.disabled)
    assert.equal(await saved.getAttribute("aria-pressed"), "true", "completion cannot change the selected log view")
    assert.equal(await page.locator("dialog[open]").count(), 0)
    if (scenario === "reread-failure") {
      await page.getByText("synthetic operation failure", { exact: true }).waitFor()
      assert.equal(await page.evaluate(() => window.jobs.length), 1, "a failed status read never invites reinstalling a committed file")
      assert.equal(await page.locator(".keybox-files").innerText().then(text => text.includes("Backup restored.")), false, "success belongs to toast and cannot shift the card")
    } else {
      await page.getByText("20 B", { exact: true }).waitFor()
      await page.waitForFunction(() => window.discards.length === 1)
    }
    assert.equal((await page.locator("body").innerText()).includes("synthetic local upload fixture"), false)
    assert.deepEqual(errors, [])
    console.log("PASS: local keybox operation — " + scenario)
    await page.close()
  }
} finally { await browser.close() }
