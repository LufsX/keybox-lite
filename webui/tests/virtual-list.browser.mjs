// PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs node tests/virtual-list.browser.mjs [url]
import assert from "node:assert/strict"
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright")
const browser = await chromium.launch({ headless: true })
const base = process.argv[2] || "http://127.0.0.1:5186"
try {
  const main = await (await fetch(base + "/src/main.ts")).text()
  const runtime = main.match(/import\s*\{\s*mount\s*\}\s*from\s*"([^"]+)"/)?.[1]
  assert.ok(runtime, "Vite must expose the Svelte mount import")
  for (const elementScroll of [false, true]) {
    const page = await browser.newPage({ viewport: { width: 390, height: 480 } })
    const errors = []
    page.on("pageerror", error => errors.push(error.message))
    await page.addInitScript(() => {
      window.virtualAnimations = 0
      window.virtualListeners = 0
      window.virtualFrames = 0
      window.virtualObservers = 0
      const animate = Element.prototype.animate
      Element.prototype.animate = function(...args) {
        if (this.matches(".virtual-row, .virtual-content")) window.virtualAnimations++
        return animate.apply(this, args)
      }
      const add = EventTarget.prototype.addEventListener
      const remove = EventTarget.prototype.removeEventListener
      EventTarget.prototype.addEventListener = function(type, listener, options) {
        if (listener?.name === "schedule" || listener?.name === "reduceMotion") window.virtualListeners++
        return add.call(this, type, listener, options)
      }
      EventTarget.prototype.removeEventListener = function(type, listener, options) {
        if (listener?.name === "schedule" || listener?.name === "reduceMotion") window.virtualListeners--
        return remove.call(this, type, listener, options)
      }
      const raf = window.requestAnimationFrame
      window.requestAnimationFrame = callback => raf(time => { window.virtualFrames++; callback(time) })
      const Observer = window.ResizeObserver
      window.ResizeObserver = class extends Observer {
        constructor(callback) { super(callback); window.virtualObservers++ }
        disconnect() { super.disconnect(); window.virtualObservers-- }
      }
    })
    await page.route("**/virtual-test", route => route.fulfill({ contentType: "text/html", body: `
      <html><head><style>body { margin: 0; overflow-anchor: none; }</style></head>
      <body><script type="module">
        import { mount } from "${runtime}";
        import Harness from "/tests/VirtualListHarness.svelte";
        mount(Harness, { target: document.body, props: { elementScroll: ${elementScroll} } });
      </script></body></html>` }))
    await page.goto(base + "/virtual-test")
    await page.waitForFunction(() => window.virtualTest)
    const change = (method, value) => page.evaluate(([method, value]) => window.virtualTest[method](value), [method, value])
    const row = key => page.locator(`[data-virtual-key="${key}"]`)
    const position = key => row(key).evaluate(node => node.getBoundingClientRect().top)
    const sample = () => page.evaluate(() => ({
      height: document.querySelector(".virtual-list").getBoundingClientRect().height,
      rows: Object.fromEntries([...document.querySelectorAll("[data-virtual-key]")].map(node => [
        node.dataset.virtualKey,
        { top: node.getBoundingClientRect().top, opacity: +getComputedStyle(node.firstElementChild).opacity },
      ])),
    }))
    const height = () => page.locator(".virtual-list").evaluate(node => node.getBoundingClientRect().height)
    const settle = () => page.waitForTimeout(300)
    await settle()
    const before = await position("user-2")
    const beforeHeight = await height()
    await change("system", true)
    await page.waitForTimeout(55)
    const insertion = await sample()
    const middle = insertion.rows["user-2"].top
    assert.ok(middle > before + 1 && middle < before + 112 - 1, `survivor insertion must interpolate: ${before}, ${middle}`)
    assert.ok(insertion.height > beforeHeight && insertion.height < beforeHeight * 2, "catalog height must interpolate")
    assert.ok(insertion.rows["sys-1"].opacity > 0 && insertion.rows["sys-1"].opacity < 1, `added row must fade in: ${JSON.stringify(insertion)}`)
    await settle()
    assert.ok(Math.abs(await position("user-2") - before - 112) < 1)
    const removeBefore = await position("sys-1")
    await change("system", false)
    await page.waitForTimeout(55)
    const removal = await sample()
    assert.ok(removal.rows["sys-1"], "outgoing row must survive long enough to fade")
    assert.ok(removal.rows["sys-1"].opacity > 0 && removal.rows["sys-1"].opacity < 1, "removed row must fade out")
    assert.ok(Math.abs(removal.rows["sys-1"].top - removeBefore) < 1, "outgoing parent must retain its offset")
    const removalMiddle = removal.rows["user-2"].top
    assert.ok(removalMiddle > before + 1 && removalMiddle < before + 111, `removal survivor: ${before}, ${removalMiddle}`)
    await settle()
    assert.equal(await page.locator('[data-virtual-key^="sys-"]').count(), 0)
    assert.equal(await height(), beforeHeight)

    // Interrupt both the move and fade, then ensure keyed rows and focus survive.
    await page.locator('[data-item="user-1"]').focus()
    for (let i = 0; i < 8; i++) {
      await change("system", i % 2 === 0)
      await page.waitForTimeout(24)
    }
    await change("system", true)
    await settle()
    assert.equal(await page.locator("[data-virtual-key]").count(), 8)
    assert.equal(await page.locator("[data-item]:focus").getAttribute("data-item"), "user-1")
    assert.deepEqual(await page.locator("[data-virtual-key]").evaluateAll(nodes => nodes.map(n => n.dataset.virtualKey).sort()),
      ["sys-0", "sys-1", "sys-2", "sys-3", "user-0", "user-1", "user-2", "user-3"])
    assert.equal(await page.locator(".virtual-content[inert]").count(), 0)
    assert.equal(await page.locator(".virtual-list").evaluate(n => n.getAnimations({ subtree: true }).length), 0)

    await change("count", 4000)
    await settle()
    const scroll = value => page.evaluate(([elementScroll, value]) => {
      if (elementScroll) document.querySelector("[data-scroll]").scrollTop = value
      else window.scrollTo(0, value)
    }, [elementScroll, value])
    const scrollValue = () => page.evaluate(elementScroll => elementScroll ? document.querySelector("[data-scroll]").scrollTop : window.scrollY, elementScroll)
    await scroll(56000)
    await settle()
    assert.ok(await page.locator("[data-virtual-key]").count() < 40, "large catalog must remain virtualized")
    assert.equal(await page.locator("[data-item]:focus").getAttribute("data-item"), "user-1", "offscreen focused row remains mounted")
    const animations = await page.evaluate(() => window.virtualAnimations)
    await scroll(60000)
    await page.waitForTimeout(80)
    await scroll(62000)
    await page.waitForTimeout(80)
    assert.equal(await page.evaluate(() => window.virtualAnimations), animations, "scroll recycling must not animate")

    // Remove a system row that currently anchors the viewport.
    await page.locator("[data-item]:focus").evaluate(node => node.blur())
    await scroll(56036 + (elementScroll ? 12 : 0) + 56)
    await settle()
    const anchor = await page.locator('[data-virtual-key^="user-"]').evaluateAll((nodes, elementScroll) => {
      const top = elementScroll ? document.querySelector("[data-scroll]").getBoundingClientRect().top + 12 : 0
      const node = nodes.find(node => node.getBoundingClientRect().top >= top)
      return { key: node.dataset.virtualKey, top: node.getBoundingClientRect().top }
    }, elementScroll)
    await change("system", false)
    await settle()
    assert.ok(Math.abs(await position(anchor.key) - anchor.top) < 1, "filtering anchor retains the next survivor's screen position")
    assert.ok(await scrollValue() > 10000, "filtering must not reset deep scroll")
    const insertionAnchor = await page.locator('[data-virtual-key^="user-"]').evaluateAll((nodes, elementScroll) => {
      const top = elementScroll ? document.querySelector("[data-scroll]").getBoundingClientRect().top + 12 : 0
      const node = nodes.find(node => node.getBoundingClientRect().top <= top && node.getBoundingClientRect().bottom > top)
      return { key: node.dataset.virtualKey, top: node.getBoundingClientRect().top }
    }, elementScroll)
    await change("system", true)
    await settle()
    assert.ok(Math.abs(await position(insertionAnchor.key) - insertionAnchor.top) < 1, "insertion above viewport preserves anchor")
    assert.ok(await page.locator("[data-virtual-key]").count() < 40)

    await change("variable", true)
    await settle()
    const rects = await page.locator("[data-virtual-key]").evaluateAll(nodes => nodes.map(node => ({
      key: node.dataset.virtualKey, index: +node.dataset.index, top: +node.style.transform.match(/[-\d.]+/)[0], height: node.getBoundingClientRect().height,
    })).sort((a, b) => a.index - b.index))
    for (let i = 1; i < rects.length; i++) if (rects[i].index === rects[i - 1].index + 1) {
      assert.ok(Math.abs(rects[i].top - rects[i - 1].top - rects[i - 1].height) < 1, "measured rows must not overlap")
    }
    const scrollBeforeReorder = await scrollValue()
    await change("reverse", true)
    await page.waitForTimeout(55)
    const fading = await page.locator(".virtual-content").evaluateAll(nodes => nodes.filter(node => {
      const opacity = +getComputedStyle(node).opacity
      return opacity > 0 && opacity < 1
    }).length)
    assert.ok(fading > 0, "sorting an entirely different viewport crossfades the recycled rows")
    assert.ok(await page.locator("[data-virtual-key]").count() < 80, "crossfades only retain mounted viewport rows")
    await settle()
    assert.ok(Math.abs(await scrollValue() - scrollBeforeReorder) < 1, "reorder must not move scroll")
    await page.emulateMedia({ reducedMotion: "reduce" })
    const reducedAnimations = await page.evaluate(() => window.virtualAnimations)
    await change("system", false)
    await page.waitForTimeout(50)
    assert.equal(await page.evaluate(() => window.virtualAnimations), reducedAnimations, "reduced motion skips row animation")
    assert.equal(await page.locator('[data-virtual-key^="sys-"]').count(), 0)
    assert.equal(await page.locator(".virtual-list").evaluate(n => n.getAnimations({ subtree: true }).length), 0)

    await change("unmount")
    await page.waitForTimeout(80)
    assert.equal(await page.evaluate(() => window.virtualListeners), 0, "unmount removes scroll, resize and media listeners")
    assert.equal(await page.evaluate(() => window.virtualObservers), 0, "unmount disconnects ResizeObserver")
    const idleFrames = await page.evaluate(() => window.virtualFrames)
    await page.waitForTimeout(320)
    assert.equal(await page.evaluate(() => window.virtualFrames), idleFrames, "unmount stops animation frames and timers")
    assert.deepEqual(errors, [])
    console.log(`PASS: ${elementScroll ? "element" : "window"} scroll, catalog motion, reversal, focus, anchoring, measurements, virtualization and cleanup`)
    await page.close()
  }
} finally { await browser.close() }
