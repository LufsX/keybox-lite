import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright")
const browser = await chromium.launch({ headless: true })
const base = process.argv[2] || "http://127.0.0.1:5186"
try {
  const page = await browser.newPage()
  const errors = []
  page.on("pageerror", error => errors.push(error.message))
  await page.addInitScript(() => {
    window.exportURLs = new Set()
    const create = URL.createObjectURL, revoke = URL.revokeObjectURL
    URL.createObjectURL = blob => { const url = create(blob); window.exportURLs.add(url); return url }
    URL.revokeObjectURL = url => { window.exportURLs.delete(url); return revoke(url) }
  })
  await page.route("**/config-export-test", route => route.fulfill({ contentType: "text/html", body: "<html><body></body></html>" }))
  await page.goto(base + "/config-export-test")
  const contents = JSON.stringify({ format: "keybox-lite.targets", version: 1, note: "中文 configuration ' \" ` $()" })
  const downloading = page.waitForEvent("download")
  const result = await page.evaluate(async contents => {
    const { saveConfiguration } = await import("/src/lib/configDownload.ts")
    return saveConfiguration("keybox-lite-targets.json", contents)
  }, contents)
  const download = await downloading
  assert.equal(download.suggestedFilename(), "keybox-lite-targets.json")
  assert.equal(await download.failure(), null)
  assert.equal(await readFile(await download.path(), "utf8"), contents)
  assert.equal(result.path, null)
  assert.equal(await page.locator("a[download]").count(), 0)
  assert.equal(await page.evaluate(() => window.exportURLs.size), 0)
  const cancelled = await page.evaluate(async contents => {
    const { saveConfiguration } = await import("/src/lib/configDownload.ts")
    const controller = new AbortController()
    controller.abort()
    try { await saveConfiguration("keybox-lite-targets.json", contents, controller.signal) }
    catch (error) { return error.code }
  }, contents)
  assert.equal(cancelled, "aborted")
  assert.equal(await page.evaluate(() => window.exportURLs.size), 0)
  assert.deepEqual(errors, [])
  console.log("PASS: real browser JSON download bytes, filename, cancellation and object URL cleanup")
} finally { await browser.close() }
