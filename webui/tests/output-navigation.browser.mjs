// PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs node tests/output-navigation.browser.mjs [url]
import assert from "node:assert/strict"
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright")
const browser = await chromium.launch()
const base = process.argv[2] || "http://127.0.0.1:5186"
try {
  for (const operation of ["update", "check", "refresh"]) {
    const page = await browser.newPage({ viewport: { width: 390, height: 844 } })
    const errors = []
    page.on("pageerror", error => errors.push(error.message))
    await page.addInitScript(operation => {
      localStorage.setItem("keybox-lite-language", "zh")
      const info = { path: "/data/adb/keybox.xml", exists: true, size: 1024, modified: 1, structure_valid: true, serials: ["1"] }
      let pending
      let finished = false
      window.logReads = 0
      window.logDelay = 20
      window.beginOperation = false
      window.completeOperation = () => { finished = true; pending?.(); pending = undefined }
      const reply = (callback, output, code = 0) => {
        const child = window[callback]
        if (!child) return
        for (const line of output.split("\n")) child.stdout.emit("data", line)
        child.emit("exit", code)
      }
      window.ksu = {
        enableEdgeToEdge() {}, toast() {},
        spawn(command, _args, _options, callback) {
          if (command.includes("tail -n 80 /data/local/tmp/keybox-update.log")) {
            const read = ++window.logReads
            setTimeout(() => reply(callback, "[SAVED] read " + read), window.logDelay)
          } else if (command.includes("for d in")) {
            setTimeout(() => reply(callback, "/data/adb/modules/keybox-lite"), 5)
          } else if (command.includes("mktemp -d /data/local/tmp/keybox-webui.")) {
            window.activeOperation = "update"
            setTimeout(() => reply(callback, "/data/local/tmp/keybox-webui.Mock123"), 5)
          } else if (command.includes("tail -c 80000")) {
            setTimeout(() => reply(callback, finished ? "0\n[UPDATE] done" : "RUNNING\n[UPDATE] running"), 5)
          } else if (command.includes(" verify ")) {
            window.activeOperation = "check"
            window[callback]?.stdout.emit("data", "[CHECK] running")
            pending = () => reply(callback, "[CHECK] status: live (https://example.test)\nRESULT: not revoked")
          } else if (command.includes(" info")) {
            if (operation === "refresh" && window.beginOperation && !finished) {
              window.activeOperation = "refresh"
              pending = () => reply(callback, JSON.stringify(info))
            } else setTimeout(() => reply(callback, JSON.stringify(info)), 5)
          } else if (command.includes("settings.sh")) {
            setTimeout(() => reply(callback, "SCHEDULE=off\nLAST_RUN=0\nSOURCE=auto\nSKIP_REVOCATION=0"), 5)
          } else setTimeout(() => reply(callback, command.includes("module.prop") ? "version=2.0.0\n---\n" : ""), 5)
        },
      }
    }, operation)
    await page.goto(base + "/")
    const current = page.getByRole("button", { name: "当前输出", exact: true })
    const saved = page.getByRole("button", { name: "更新日志", exact: true })
    await page.waitForFunction(() => [...document.querySelectorAll("button")].some(button => button.textContent.trim() === "刷新" && !button.disabled))
    await saved.click()
    await page.waitForFunction(() => document.querySelector('pre[aria-label="更新日志"]')?.textContent.includes("[SAVED] read 1"))
    await current.click()
    await page.evaluate(() => { window.logDelay = 1200; window.beginOperation = true })
    const labels = { update: "更新 keybox", check: "证书检测", refresh: "刷新" }
    await page.getByRole("button", { name: labels[operation], exact: true }).click()
    await page.waitForFunction(operation => window.activeOperation === operation, operation)
    assert.equal(await saved.isEnabled(), true, operation + " must not disable log navigation")
    await saved.click()
    assert.equal(await saved.getAttribute("aria-pressed"), "true", "cached output selects immediately")
    assert.ok((await page.locator('pre[aria-label="更新日志"]').last().innerText()).includes("[SAVED] read 1"))
    await current.click()
    assert.equal(await current.getAttribute("aria-pressed"), "true")
    await saved.click()
    assert.equal(await saved.getAttribute("aria-pressed"), "true", "pending reads do not block selecting saved output")
    assert.equal(await page.evaluate(() => window.logReads), 2, "view changes share the pending read")
    await page.evaluate(() => window.completeOperation())
    await page.waitForFunction(() => [...document.querySelectorAll("button")].some(button => button.textContent.trim() === "刷新" && !button.disabled))
    assert.equal(await saved.getAttribute("aria-pressed"), "true", "operation completion preserves the chosen output")
    await current.click()
    await page.waitForFunction(() => document.querySelector(".log-views button:last-child").getAttribute("aria-busy") === "false")
    assert.equal(await current.getAttribute("aria-pressed"), "true", "late log completion preserves current output")
    if (operation !== "refresh") {
      const text = await page.locator('pre[aria-label="当前输出"]').last().innerText()
      assert.ok(text.includes(operation === "update" ? "[UPDATE] done" : "RESULT: not revoked"), text)
    }
    assert.deepEqual(errors, [])
    await page.close()
    console.log("PASS: " + operation + ", cached output, pending navigation, shared reads and live output")
  }
} finally { await browser.close() }
