// PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs node tests/log-output.browser.mjs [url]
import assert from "node:assert/strict"
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright")
const browser = await chromium.launch()
const base = process.argv[2] || "http://127.0.0.1:5186"
try {
  const main = await (await fetch(base + "/src/main.ts")).text()
  const runtime = main.match(/import\s*\{\s*mount\s*\}\s*from\s*"([^"]+)"/)?.[1]
  assert.ok(runtime)
  const page = await browser.newPage({ viewport: { width: 390, height: 844 } })
  const errors = []
  page.on("pageerror", error => errors.push(error.message))
  await page.route("**/log-test", route => route.fulfill({ contentType: "text/html", body: `
    <html><body class="app-shell"><script type="module">
      import { mount } from "${runtime}";
      import Harness from "/tests/LogOutputHarness.svelte";
      import "/src/app.css";
      mount(Harness, { target: document.body });
    </script></body></html>` }))
  await page.goto(base + "/log-test")
  const current = page.getByRole("button", { name: "Current", exact: true })
  const saved = page.getByRole("button", { name: "Saved", exact: true })
  assert.equal(await page.getByRole("button", { name: "Refresh", exact: true }).count(), 0)
  await current.waitFor()
  await page.waitForTimeout(240)
  const small = await page.locator(".log-content").evaluate(node => node.getBoundingClientRect().height)
  // Sample every frame to detect even short loading flashes.
  await page.evaluate(() => {
    window.loadingFrames = 0
    window.observeLoading = true
    const sample = () => {
      if (!window.observeLoading) return
      if (document.querySelector(".busy-status.is-visible")) window.loadingFrames++
      requestAnimationFrame(sample)
    }
    requestAnimationFrame(sample)
  })
  const initialIndicator = await page.locator(".log-view-indicator").boundingBox()
  await saved.click()
  await page.waitForFunction(() => document.querySelector(".log-views button:last-child").getAttribute("aria-pressed") === "true")
  await page.waitForTimeout(45)
  const movingIndicator = await page.locator(".log-view-indicator").boundingBox()
  const savedBounds = await saved.boundingBox()
  assert.ok(movingIndicator.x > initialIndicator.x + 1 && movingIndicator.x < savedBounds.x - 1, "the output selector slides between its choices")
  await page.waitForTimeout(280)
  const settledIndicator = await page.locator(".log-view-indicator").boundingBox()
  assert.ok(Math.abs(settledIndicator.x - savedBounds.x) < 1 && Math.abs(settledIndicator.width - savedBounds.width) < 1, "the sliding surface fits the selected label")
  assert.equal(await page.evaluate(() => window.loadingFrames), 0, "fast reads never display a spinner")
  assert.equal(await page.locator(".log-content").evaluate(node => node.getBoundingClientRect().height), 300)
  assert.equal(await page.locator("pre").count(), 1)
  await page.locator("pre").evaluate(node => { node.scrollTop = 120 })
  await current.click()
  await page.waitForTimeout(50)
  const middle = await page.locator(".log-content").evaluate(node => node.getBoundingClientRect().height)
  assert.ok(middle > small && middle < 300, "height interpolates during a view change")
  await page.waitForTimeout(240)
  assert.equal(await page.locator(".log-content").evaluate(node => node.getBoundingClientRect().height), small)
  await saved.click()
  await page.waitForTimeout(280)
  assert.equal(await page.locator("pre").evaluate(node => node.scrollTop), 120)
  await page.evaluate(() => { window.latency = 220 })
  await saved.click()
  await page.locator(".busy-status.is-visible").waitFor()
  const shownAt = Date.now()
  await page.waitForFunction(() => !document.querySelector("pre[aria-busy=true]"))
  assert.equal(await page.locator(".busy-status.is-visible").count(), 1, "indicator does not vanish immediately after completion")
  await page.waitForFunction(() => !document.querySelector(".busy-status.is-visible"))
  assert.ok(Date.now() - shownAt >= 220, "slow read indicator has a stable visible interval")
  await page.evaluate(() => { window.observeLoading = false })
  for (let index = 0; index < 4; index++) {
    await current.click()
    await page.evaluate(() => { window.latency = 0 })
    await saved.click()
  }
  await page.waitForTimeout(280)
  assert.equal(await page.locator("pre").count(), 1, "rapid switching leaves one output panel")
  assert.equal(await page.locator("pre[id]").count(), 1)
  assert.equal(await saved.getAttribute("aria-pressed"), "true")
  await page.emulateMedia({ reducedMotion: "reduce" })
  await current.click()
  await page.waitForTimeout(20)
  assert.equal(await page.locator(".log-content").evaluate(node => getComputedStyle(node).transitionDuration), "0s")
  assert.equal(await page.locator(".log-view-indicator").evaluate(node => getComputedStyle(node).transitionDuration), "0s")
  await page.setViewportSize({ width: 320, height: 844 })
  await page.waitForTimeout(50)
  const narrowIndicator = await page.locator(".log-view-indicator").boundingBox()
  const currentBounds = await current.boundingBox()
  assert.ok(Math.abs(narrowIndicator.x - currentBounds.x) < 1 && Math.abs(narrowIndicator.width - currentBounds.width) < 1)
  assert.deepEqual(errors, [])
  console.log("PASS: smooth log view heights, scroll restoration, fast/slow refresh indicators, rapid switches and reduced motion")
} finally {
  await browser.close()
}
