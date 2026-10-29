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
  assert.equal(await page.locator("pre").evaluate(node => node.scrollTop), 0, "saved logs initially open for static reading")
  await page.locator("pre").evaluate(node => { node.scrollTop = 120 })
  await current.click()
  await page.waitForTimeout(50)
  const middle = await page.locator(".log-content").evaluate(node => node.getBoundingClientRect().height)
  assert.ok(middle > small && middle < 300, "height interpolates during a view change")
  await page.waitForTimeout(240)
  assert.ok(Math.abs(await page.locator(".log-content").evaluate(node => node.getBoundingClientRect().height) - small) < 1, "height settles back to the compact current output")
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
  await page.evaluate(() => { window.observeLoading = false; window.latency = 1000 })
  await saved.click()
  const reads = await page.evaluate(() => window.savedReads)
  assert.equal(await saved.isEnabled(), true, "pending logs remain selectable")
  await current.click()
  assert.equal(await current.getAttribute("aria-pressed"), "true")
  await page.waitForTimeout(30)
  assert.equal(await page.locator('pre[aria-label="Current"]').getAttribute("aria-busy"), "false", "current output is independent of the saved-log read")
  await saved.click()
  assert.equal(await saved.getAttribute("aria-pressed"), "true", "cached logs select before the read completes")
  assert.equal(await page.evaluate(() => window.savedReads), reads, "switching back shares the pending read")
  await current.click()
  await page.waitForFunction(() => document.querySelector(".log-views button:last-child").getAttribute("aria-busy") === "false")
  assert.equal(await current.getAttribute("aria-pressed"), "true", "late read completion cannot replace the current view")
  assert.equal(await page.locator("pre").count(), 1)
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
  const currentBody = page.locator('pre[aria-label="Current"]:not([aria-hidden="true"])')
  const savedBody = page.locator('pre[aria-label="Saved"]:not([aria-hidden="true"])')
  const back = page.getByRole("button", { name: "Back to bottom", exact: true })
  const rows = (prefix, count = 90) => Array.from({ length: count }, (_, index) =>
    prefix + " " + index + " — a live log line with enough detail to wrap on narrow screens").join("\n")
  const mutate = (method, value) => page.evaluate(({ method, value }) => window.logHarness[method](value), { method, value })
  const expectBottom = () => page.waitForFunction(() => {
    const node = document.querySelector('pre[aria-label="Current"]:not([aria-hidden="true"])')
    return node && node.scrollHeight > node.clientHeight &&
      node.scrollHeight - node.clientHeight - node.scrollTop <= 2
  })
  const expectPosition = async (body, top, message) => {
    await page.waitForTimeout(50)
    assert.ok(Math.abs(await body.evaluate(node => node.scrollTop) - top) <= 1, message)
  }
  for (const reduced of [false, true]) {
    await page.emulateMedia({ reducedMotion: reduced ? "reduce" : "no-preference" })
    await page.setViewportSize({ width: 390, height: 844 })
    await page.evaluate(() => { window.latency = 0 })
    await mutate("replaceCurrent", "")
    await page.waitForFunction(() => document.querySelector('pre[aria-label="Current"]')?.textContent === "No output")
    assert.equal(await back.count(), 0)
    await mutate("replaceCurrent", rows("Current task"))
    await expectBottom()
    await page.waitForTimeout(300)
    assert.equal(await page.locator(".log-content").evaluate(node => node.getBoundingClientRect().height), 300)
    const originalHeight = await currentBody.evaluate(node => node.clientHeight)
    for (let index = 0; index < 3; index++) {
      await mutate("appendCurrent", "\n" + rows("Streaming " + index, 5))
      await expectBottom()
    }
    assert.ok((await currentBody.textContent()).includes("Streaming 2 4"), "new lines render during following")
    assert.equal(await back.count(), 0, "programmatic following does not show the paused button")

    await currentBody.hover()
    await page.mouse.wheel(0, -170)
    await back.waitFor()
    const readingTop = await currentBody.evaluate(node => node.scrollTop)
    assert.ok(readingTop > 0, "wheel scrolling moves away from the live tail")
    assert.equal(await currentBody.evaluate(node => node.clientHeight), originalHeight, "the back button adds no log height")
    await mutate("appendCurrent", "\n" + rows("Paused append", 8))
    await expectPosition(currentBody, readingTop, "appending while paused preserves the reading position")
    assert.ok((await currentBody.textContent()).includes("Paused append 7"), "new content renders while following is paused")

    await mutate("setBottomLabel", "回到底部")
    const translatedBack = page.getByRole("button", { name: "回到底部", exact: true })
    await translatedBack.waitFor()
    await translatedBack.click()
    await expectBottom()
    assert.equal(await translatedBack.count(), 0, "clicking back resumes following immediately")
    await mutate("setBottomLabel", undefined)
    await mutate("appendCurrent", "\n" + rows("Resumed append", 4))
    await expectBottom()

    // Scroll events update the button without waiting for another content change.
    await currentBody.evaluate(node => { node.scrollTop = node.scrollHeight - node.clientHeight - 40 })
    await back.waitFor()
    await currentBody.evaluate(node => { node.scrollTop = node.scrollHeight - node.clientHeight - 1 })
    await page.waitForFunction(() => !document.querySelector(".log-bottom"))
    await mutate("appendCurrent", "\nnear-bottom append")
    await expectBottom()

    // Content and user scrolling can arrive before the browser dispatches scroll.
    await page.evaluate(() => {
      const node = document.querySelector('pre[aria-label="Current"]:not([aria-hidden="true"])')
      node.scrollTop = 140
      window.logHarness.appendCurrent("\nsame-frame append")
    })
    await back.waitFor()
    await expectPosition(currentBody, 140, "a same-frame content update cannot overwrite user scrolling")
    await currentBody.hover()
    const streaming = page.evaluate(async () => {
      for (let index = 0; index < 24; index++) {
        window.logHarness.appendCurrent("\ncontinuous stream " + index)
        await new Promise(resolve => requestAnimationFrame(resolve))
      }
    })
    await page.mouse.wheel(0, -45)
    await streaming
    const streamingTop = await currentBody.evaluate(node => node.scrollTop)
    assert.ok(streamingTop < 140, "reading remains scrollable during continuous output")
    assert.equal(await back.count(), 1)
    await expectPosition(currentBody, streamingTop, "streaming does not reclaim a paused scroll position")
    await back.click()
    await expectBottom()

    await saved.click()
    await savedBody.waitFor()
    assert.equal(await back.count(), 0, "saved logs do not expose live following")
    await page.waitForTimeout(300)
    await savedBody.evaluate(node => { node.scrollTop = 123 })
    await expectPosition(savedBody, 123, "saved logs can be read independently")
    await mutate("appendCurrent", "\n" + rows("Hidden following", 10))
    await expectPosition(savedBody, 123, "hidden current updates do not disturb saved reading")
    await current.click()
    await expectBottom()
    await page.waitForTimeout(300)
    assert.ok((await currentBody.textContent()).includes("Hidden following 9"))

    await currentBody.evaluate(node => { node.scrollTop = 155 })
    await back.waitFor()
    await saved.click()
    await savedBody.waitFor()
    await page.waitForTimeout(300)
    await expectPosition(savedBody, 123, "saved position survives switching")
    await mutate("appendCurrent", "\n" + rows("Hidden paused", 6))
    await current.click()
    await currentBody.waitFor()
    await back.waitFor()
    await page.waitForTimeout(300)
    await expectPosition(currentBody, 155, "hidden appends preserve paused current reading")
    assert.ok((await currentBody.textContent()).includes("Hidden paused 5"))

    await saved.click()
    await savedBody.waitFor()
    await page.waitForTimeout(300)
    await savedBody.evaluate(node => { node.scrollTop = node.scrollHeight })
    const savedTail = await savedBody.evaluate(node => node.scrollTop)
    await mutate("appendSaved", "\n" + rows("Static saved append", 5))
    await expectPosition(savedBody, savedTail, "saved log updates remain static even when previously at the bottom")
    assert.ok(await savedBody.evaluate(node => node.scrollHeight - node.clientHeight - node.scrollTop > 2))
    await current.click()
    await currentBody.waitFor()
    await page.waitForTimeout(300)
    await expectPosition(currentBody, 155, "saved scrolling cannot overwrite current position")

    await page.setViewportSize({ width: 320, height: 844 })
    await expectPosition(currentBody, 155, "reflow preserves a paused reading position")
    await back.click()
    await expectBottom()
    await page.setViewportSize({ width: 390, height: 844 })
    await expectBottom()
    await mutate("appendCurrent", "\nresize append")
    await expectBottom()

    await currentBody.evaluate(node => { node.scrollTop = 100 })
    await back.waitFor()
    await mutate("replaceCurrent", "")
    await page.waitForFunction(() => document.querySelector('pre[aria-label="Current"]')?.textContent === "No output")
    assert.equal(await back.count(), 0, "clearing a task resets paused following")
    await page.waitForTimeout(300)
    assert.ok(await page.locator(".log-content").evaluate(node => node.getBoundingClientRect().height < 50), "an empty task returns to compact height")
    await mutate("appendCurrent", rows("New task"))
    await expectBottom()
    await currentBody.evaluate(node => { node.scrollTop = 100 })
    await back.waitFor()
    await saved.click()
    await savedBody.waitFor()
    await mutate("replaceCurrent", "")
    await current.click()
    await page.waitForFunction(() => document.querySelector('pre[aria-label="Current"]:not([aria-hidden="true"])')?.textContent === "No output")
    assert.equal(await back.count(), 0, "a task cleared while hidden also resets following")
    await mutate("appendCurrent", rows("New hidden task"))
    await expectBottom()

    for (let index = 0; index < 5; index++) {
      await saved.click()
      await mutate("appendCurrent", "\nrapid hidden append " + index)
      await current.click()
      await mutate("appendCurrent", "\nrapid current append " + index)
    }
    await expectBottom()
    await page.waitForTimeout(300)
    assert.equal(await page.locator("pre").count(), 1, "rapid keyed switches retire outgoing nodes")
    assert.equal(await page.locator("pre[id]").count(), 1)
    assert.equal(await page.locator('pre[inert], pre[aria-hidden="true"]').count(), 0)
    assert.equal(await back.count(), 0, "restoration during rapid switches cannot pause following")
    await mutate("appendCurrent", "\nfinal live tail")
    await expectBottom()

    // Destroy while content/layout work is queued, then mount a fresh instance.
    await page.evaluate(() => {
      window.logHarness.appendCurrent("\nunmount pending")
      window.logHarness.setMounted(false)
    })
    await page.waitForFunction(() => !document.querySelector("pre"))
    await mutate("setMounted", true)
    await expectBottom()
    await page.waitForTimeout(300)
    assert.equal(await page.locator("pre").count(), 1)
  }
  assert.deepEqual(errors, [])
  console.log("PASS: live following, pause/resume, hidden updates, independent saved reading, task reset, resize, rapid switches, cleanup, view heights, refresh indicators and reduced motion")
} finally {
  await browser.close()
}
