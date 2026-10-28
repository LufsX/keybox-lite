// Run against Vite: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs node tests/dropdown.browser.mjs [url]
import assert from "node:assert/strict"
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright")
const browser = await chromium.launch({ headless: true })
const base = process.argv[2] || "http://127.0.0.1:5186"
try {
  const main = await (await fetch(base + "/src/main.ts")).text()
  const runtime = main.match(/import\s*\{\s*mount\s*\}\s*from\s*"([^"]+)"/)?.[1]
  assert.ok(runtime, "Vite must expose the Svelte mount import")
  for (const fallback of [false, true]) {
    const page = await browser.newPage({ viewport: { width: 360, height: 480 } })
    const errors = []
    page.on("pageerror", error => errors.push(error.message))
    await page.addInitScript(() => {
      window.floatingListeners = 0
      window.floatingReads = 0
      const add = EventTarget.prototype.addEventListener
      const remove = EventTarget.prototype.removeEventListener
      EventTarget.prototype.addEventListener = function(type, listener, options) {
        if (listener?.name === "schedule") window.floatingListeners++
        return add.call(this, type, listener, options)
      }
      EventTarget.prototype.removeEventListener = function(type, listener, options) {
        if (listener?.name === "schedule") window.floatingListeners--
        return remove.call(this, type, listener, options)
      }
      const rect = HTMLElement.prototype.getBoundingClientRect
      HTMLElement.prototype.getBoundingClientRect = function() {
        if (this.matches(".test-trigger, dialog")) window.floatingReads++
        return rect.call(this)
      }
    })
    if (fallback) await page.addInitScript(() => {
      Object.defineProperty(HTMLElement.prototype, "showPopover", { value: undefined, configurable: true })
    })
    await page.route("**/dropdown-test", route => route.fulfill({ contentType: "text/html", body: `
      <html><head><style>:root { --page-gutter: 12px; --content-padding: 8px; --line: #888; --panel: white; --text: #222; --muted: #555; --inset: #eee; } body { margin: 0; }</style></head>
      <body><script type="module">
        import { mount } from "${runtime}";
        import Harness from "/tests/DropdownHarness.svelte";
        import "/src/app.css";
        mount(Harness, { target: document.body });
      </script></body></html>` }))
    await page.goto(base + "/dropdown-test")
    const trigger = page.getByRole("button", { name: "Mode", exact: true })
    await trigger.waitFor()
    await page.waitForTimeout(180)
    const dimensions = () => page.locator("dialog").evaluate(dialog => ({
      height: dialog.getBoundingClientRect().height,
      scroll: dialog.querySelector("[data-modal-scroll]").scrollHeight,
    }))
    const before = await dimensions()
    await trigger.click()
    const menu = page.locator("#test-options")
    await menu.waitFor({ state: "visible" })
    await page.waitForTimeout(180)
    assert.deepEqual(await dimensions(), before, "opening must not change dialog or scroll height")
    assert.equal(await menu.evaluate(node => node.matches(":popover-open")), !fallback)
    const checkBounds = async () => {
      const r = await menu.boundingBox()
      const viewport = page.viewportSize()
      assert.ok(r.x >= 7 && r.y >= 7 && r.x + r.width <= viewport.width - 7 && r.y + r.height <= viewport.height - 7, JSON.stringify(r))
    }
    await checkBounds()
    const reads = await page.evaluate(() => window.floatingReads)
    await page.waitForTimeout(120)
    assert.equal(await page.evaluate(() => window.floatingReads), reads, "idle open menus must not poll layout")
    // The anchor moves in the modal's scroll container.
    await page.locator("[data-modal-scroll]").evaluate(node => { node.scrollTop += 28 })
    await page.waitForTimeout(40)
    await checkBounds()
    const origin = await menu.evaluate(node => node.style.transformOrigin)
    assert.equal(origin, "right bottom", "lower trigger should flip upward")
    await page.setViewportSize({ width: 320, height: 400 })
    await page.waitForTimeout(40)
    await checkBounds()
    await page.keyboard.press("Escape")
    await menu.waitFor({ state: "detached" })
    assert.equal(await page.locator("dialog[open]").count(), 1, "Escape first closes menu")
    assert.equal(await page.evaluate(() => window.floatingListeners), 0, "close must release viewport and scroll listeners")
    await trigger.press("ArrowDown")
    await menu.waitFor({ state: "visible" })
    assert.equal(await page.locator("[data-option]:focus").innerText(), "Option 1")
    await page.keyboard.press("End")
    assert.equal(await page.locator("[data-option]:focus").innerText(), "Option 11")
    await page.keyboard.press("ArrowDown")
    assert.equal(await page.locator("[data-option]:focus").innerText(), "Option 0")
    await page.keyboard.press("Enter")
    await menu.waitFor({ state: "detached" })
    assert.equal(await trigger.innerText(), "Option 0")
    await trigger.click()
    await menu.waitFor({ state: "visible" })
    await page.locator("[data-outside]").click()
    await menu.waitFor({ state: "detached" })
    assert.equal(await page.locator("dialog[open]").count(), 1)
    // Rapid close/reopen exercises action cleanup before its first animation frame.
    await trigger.evaluate(button => { button.click(); queueMicrotask(() => button.click()) })
    await page.waitForTimeout(80)
    await trigger.click()
    await menu.waitFor({ state: "visible" })
    await page.keyboard.press("Escape")
    await page.keyboard.press("Escape")
    await page.locator("dialog").waitFor({ state: "detached" })
    assert.deepEqual(errors, [])
    await page.close()
    console.log(fallback ? "dialog fallback passed" : "popover top layer passed")
  }
} finally { await browser.close() }
