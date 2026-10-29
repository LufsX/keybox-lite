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
      window.floatingObservers = 0
      window.motionListeners = 0
      window.floatingReads = 0
      const names = new Set(["schedule", "animationFinished", "dialogStateChanged"])
      const add = EventTarget.prototype.addEventListener
      const remove = EventTarget.prototype.removeEventListener
      const registrations = new WeakMap()
      function track(target, type, listener, options, adding) {
        if (!names.has(listener?.name) && listener?.name !== "dropdownMotionChanged") return
        const capture = typeof options === "boolean" ? options : !!options?.capture
        let entries = registrations.get(target)
        if (!entries) registrations.set(target, entries = [])
        const index = entries.findIndex(entry => entry.type === type && entry.listener === listener && entry.capture === capture)
        if (adding ? index >= 0 : index < 0) return
        if (adding) entries.push({ type, listener, capture })
        else entries.splice(index, 1)
        if (names.has(listener.name)) window.floatingListeners += adding ? 1 : -1
        else window.motionListeners += adding ? 1 : -1
      }
      EventTarget.prototype.addEventListener = function(type, listener, options) {
        track(this, type, listener, options, true)
        return add.call(this, type, listener, options)
      }
      EventTarget.prototype.removeEventListener = function(type, listener, options) {
        track(this, type, listener, options, false)
        return remove.call(this, type, listener, options)
      }
      for (const name of ["ResizeObserver", "MutationObserver"]) {
        const Observer = window[name]
        window[name] = class extends Observer {
          constructor(callback) {
            super(callback)
            this.tracked = names.has(callback.name)
            this.active = false
          }
          observe(...args) {
            if (this.tracked && !this.active) { this.active = true; window.floatingObservers++ }
            return super.observe(...args)
          }
          disconnect() {
            if (this.active) { this.active = false; window.floatingObservers-- }
            return super.disconnect()
          }
        }
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
    await page.route("**/dropdown-test*", route => {
      const params = new URL(route.request().url()).searchParams
      return route.fulfill({ contentType: "text/html", body: `
        <html><head><style>:root { --page-gutter: 12px; --content-padding: 8px; --line: #888; --panel: white; --text: #222; --muted: #555; --inset: #eee; } body { margin: 0; }</style></head>
        <body><script type="module">
          import { mount, flushSync, unmount } from "${runtime}";
          import Harness from "/tests/DropdownHarness.svelte";
          import "/src/app.css";
          window.flushSync = flushSync;
          window.harness = mount(Harness, { target: document.body, props: {
            standalone: ${params.has("standalone")}, multiple: ${params.has("multiple")}
          } });
          window.destroyHarness = (outro) => unmount(window.harness, { outro });
        </script></body></html>` })
    })
    const trigger = page.getByRole("button", { name: "Mode", exact: true })
    const menu = page.locator("#test-options")
    const configure = state => page.evaluate(state => window.flushSync(() => window.harness.configure(state)), state)
    const load = async (query = "") => {
      await page.goto(base + "/dropdown-test" + query)
      await trigger.waitFor()
      await page.waitForFunction(() => !document.querySelector("dialog")?.getAnimations().some(animation => animation.playState === "running"))
    }
    const settledOpen = async () => {
      await menu.waitFor({ state: "visible" })
      await page.waitForFunction(() => {
        const node = document.querySelector("#test-options")
        return node && !node.inert && !node.getAnimations().length
      })
    }
    const checkBounds = async () => {
      const r = await menu.boundingBox()
      const viewport = page.viewportSize()
      assert.ok(r && r.x >= 7 && r.y >= 7 && r.x + r.width <= viewport.width - 7 && r.y + r.height <= viewport.height - 7, JSON.stringify(r))
    }
    const assertClean = async (motionListeners) => {
      assert.deepEqual(await page.evaluate(() => ({
        menus: document.querySelectorAll(".dropdown-menu").length,
        popovers: document.querySelectorAll(":popover-open").length,
        listeners: window.floatingListeners,
        observers: window.floatingObservers,
      })), { menus: 0, popovers: 0, listeners: 0, observers: 0 })
      if (motionListeners !== undefined) assert.equal(await page.evaluate(() => window.motionListeners), motionListeners)
      const reads = await page.evaluate(() => {
        const reads = window.floatingReads
        window.dispatchEvent(new Event("resize"))
        window.dispatchEvent(new Event("scroll"))
        window.visualViewport?.dispatchEvent(new Event("resize"))
        return reads
      })
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
      assert.equal(await page.evaluate(() => window.floatingReads), reads, "disposed menus must not schedule layout")
    }
    // Pause the actual surface animation at a known point, avoiding timing-dependent screenshots.
    const freezeExit = async action => {
      await action()
      await page.waitForFunction(() => {
        const node = document.querySelector("#test-options")
        const animation = node?.getAnimations().find(animation => Number(animation.effect.getTiming().duration) > 0)
        if (!node?.inert || node.getAttribute("aria-hidden") !== "true" || !animation) return false
        animation.pause()
        animation.currentTime = Number(animation.effect.getTiming().duration) * .4
        window.exitMenu = node
        window.exitAnimation = animation
        return true
      })
      const state = await menu.evaluate(node => ({
        connected: node.isConnected,
        inert: node.inert,
        hidden: node.getAttribute("aria-hidden"),
        pointerEvents: getComputedStyle(node).pointerEvents,
        opacity: Number(getComputedStyle(node).opacity),
        scale: new DOMMatrixReadOnly(getComputedStyle(node).transform).a,
        popover: node.matches(":popover-open"),
        listeners: window.floatingListeners,
        observers: window.floatingObservers,
      }))
      assert.equal(state.connected, true, "exit must retain the menu")
      assert.equal(state.inert, true, "exit must disable interaction")
      assert.equal(state.hidden, "true")
      assert.equal(state.pointerEvents, "none")
      assert.ok(state.opacity > 0 && state.opacity < 1, JSON.stringify(state))
      assert.ok(state.scale > .98 && state.scale < 1, JSON.stringify(state))
      assert.equal(state.popover, !fallback, "Popover must survive until exit completes")
      assert.ok(state.listeners > 0 && state.observers > 0, "positioning must remain active during exit")
      return state
    }
    const finishExit = async () => {
      await page.evaluate(() => window.exitAnimation.play())
      await menu.waitFor({ state: "detached" })
      await assertClean()
    }

    await load()
    const dimensions = () => page.locator("dialog").evaluate(dialog => ({
      height: dialog.getBoundingClientRect().height,
      scroll: dialog.querySelector("[data-modal-scroll]").scrollHeight,
    }))
    const before = await dimensions()
    await trigger.click()
    await settledOpen()
    assert.deepEqual(await dimensions(), before, "opening must not change dialog or scroll height")
    assert.equal(await menu.evaluate(node => node.matches(":popover-open")), !fallback)
    await checkBounds()
    const reads = await page.evaluate(() => window.floatingReads)
    await page.waitForTimeout(120)
    assert.equal(await page.evaluate(() => window.floatingReads), reads, "idle open menus must not poll layout")
    await page.locator("[data-modal-scroll]").evaluate(node => { node.scrollTop += 28 })
    await page.waitForTimeout(40)
    await checkBounds()
    assert.equal(await menu.evaluate(node => node.style.transformOrigin), "right bottom", "lower trigger should flip upward")
    await page.setViewportSize({ width: 320, height: 400 })
    await page.waitForTimeout(40)
    await checkBounds()

    await freezeExit(() => page.keyboard.press("Escape"))
    assert.equal(await trigger.getAttribute("aria-expanded"), "false")
    assert.equal(await trigger.evaluate(node => node === document.activeElement), true)
    assert.equal(await page.locator("dialog[open]").count(), 1, "Escape first closes menu")
    const placement = await menu.evaluate(node => node.style.top + node.style.maxHeight)
    await page.locator("[data-modal-scroll]").evaluate(node => { node.scrollTop += node.scrollTop > 0 ? -12 : 12 })
    await page.waitForFunction(placement => {
      const node = document.querySelector("#test-options")
      return node.style.top + node.style.maxHeight !== placement
    }, placement)
    await checkBounds()
    await finishExit()
    assert.equal(await page.locator("dialog").evaluate(node => node.style.overflow), "")

    await trigger.press("ArrowDown")
    await settledOpen()
    assert.equal(await page.locator("[data-option]:focus").innerText(), "Option 1")
    await page.keyboard.press("End")
    assert.equal(await page.locator("[data-option]:focus").innerText(), "Option 11")
    await page.keyboard.press("ArrowDown")
    assert.equal(await page.locator("[data-option]:focus").innerText(), "Option 0")
    await freezeExit(() => page.keyboard.press("Enter"))
    assert.equal(await trigger.innerText(), "Option 0")
    assert.equal(await trigger.evaluate(node => node === document.activeElement), true)
    // Inert blocks real input; the callback guard also rejects stale synthetic selection.
    await menu.evaluate(node => {
      node.querySelectorAll("[data-option]")[4].click()
      node.querySelector("[data-option]").focus()
    })
    assert.equal(await trigger.innerText(), "Option 0")
    assert.equal(await trigger.evaluate(node => node === document.activeElement), true)
    await finishExit()

    await trigger.click()
    await settledOpen()
    await freezeExit(() => page.locator("[data-outside]").click())
    assert.equal(await page.locator("[data-outside]").evaluate(node => node === document.activeElement), true, "outside clicks keep their own focus")
    await finishExit()

    await trigger.click()
    await settledOpen()
    await menu.locator("[data-option]").first().focus()
    const closing = await freezeExit(async () => {
      const immediate = await page.evaluate(() => {
        const node = document.querySelector("#test-options")
        window.flushSync(() => window.harness.configure({ open: false }))
        return { connected: node.isConnected, inert: node.inert, pointerEvents: getComputedStyle(node).pointerEvents }
      })
      assert.deepEqual(immediate, { connected: true, inert: true, pointerEvents: "none" }, "closing must disable input in the same update")
    })
    assert.equal(await trigger.evaluate(node => node === document.activeElement), true, "external close restores menu focus")
    await configure({ open: true })
    await page.waitForFunction(() => {
      const node = document.querySelector("#test-options")
      const animation = node?.getAnimations().find(animation => animation !== window.exitAnimation && Number(animation.effect.getTiming().duration) > 0)
      if (node?.inert || !animation) return false
      animation.pause()
      animation.currentTime = 0
      window.reverseAnimation = animation
      return true
    })
    assert.equal(await menu.evaluate(node => node === window.exitMenu), true, "reopen must reuse the exiting menu")
    assert.equal(await page.locator("#test-options").count(), 1)
    assert.equal(await menu.getAttribute("aria-hidden"), null)
    assert.ok(Math.abs(await menu.evaluate(node => Number(getComputedStyle(node).opacity)) - closing.opacity) < .03, "reversal must start at the current opacity")
    await page.evaluate(() => window.reverseAnimation.play())
    await settledOpen()
    await page.waitForTimeout(160)
    assert.equal(await menu.count(), 1, "cancelled exit must not remove the reopened menu")

    await freezeExit(() => configure({ disabled: true }))
    assert.equal(await trigger.isDisabled(), true)
    await finishExit()
    await configure({ disabled: false })
    assert.equal(await menu.count(), 0, "re-enabling must not resurrect a disabled menu")

    // Close during the intro, then reverse again before the exit completes.
    await configure({ open: true })
    await page.waitForFunction(() => {
      const node = document.querySelector("#test-options")
      const animation = node?.getAnimations().find(animation => Number(animation.effect.getTiming().duration) > 0)
      if (!animation || node.inert) return false
      animation.pause()
      animation.currentTime = Number(animation.effect.getTiming().duration) * .35
      return true
    })
    await freezeExit(() => configure({ open: false }))
    await configure({ open: true })
    await settledOpen()
    await configure({ open: false })
    await menu.waitFor({ state: "detached" })
    await assertClean()
    // Destroy an action before its first positioning frame.
    await page.evaluate(async () => {
      window.flushSync(() => window.harness.configure({ open: true }))
      await Promise.resolve()
      window.flushSync(() => window.harness.configure({ open: false, reducedMotion: true }))
    })
    await menu.waitFor({ state: "detached" })
    await assertClean()

    for (const preference of ["prop", "system"]) {
      if (preference === "prop") await configure({ reducedMotion: true })
      else { await configure({ reducedMotion: false }); await page.emulateMedia({ reducedMotion: "reduce" }) }
      await configure({ open: true })
      await settledOpen()
      await configure({ open: false })
      assert.equal(await menu.count(), 0, "reduced motion must remove the menu without a timed exit")
      await assertClean()
      assert.equal(await trigger.getAttribute("aria-expanded"), "false")
      if (preference === "prop") await configure({ reducedMotion: false })
      else await page.emulateMedia({ reducedMotion: "no-preference" })
      await configure({ open: true })
      await settledOpen()
      await freezeExit(() => configure({ open: false }))
      if (preference === "prop") await configure({ reducedMotion: true })
      else await page.emulateMedia({ reducedMotion: "reduce" })
      await menu.waitFor({ state: "detached" })
      await assertClean()
      await configure({ reducedMotion: false })
      await page.emulateMedia({ reducedMotion: "no-preference" })
    }

    // A parent conditional must retain the global exit and release component listeners afterwards.
    await configure({ open: true })
    await settledOpen()
    await freezeExit(() => configure({ mounted: false }))
    await finishExit()
    await assertClean(0)

    await load()
    await trigger.click()
    await settledOpen()
    await freezeExit(() => configure({ visible: false }))
    assert.equal(await page.locator("dialog[open]").count(), 1, "parent removal must retain the dialog and its Popover during exit")
    await finishExit()
    await page.locator("dialog").waitFor({ state: "detached" })
    await assertClean(0)

    await load()
    await trigger.click()
    await settledOpen()
    await freezeExit(() => page.locator("[data-outside]").focus())
    assert.equal(await page.locator("[data-outside]").evaluate(node => node === document.activeElement), true, "focus dismissal must keep the new focus")
    await finishExit()
    await trigger.click()
    await settledOpen()
    // Synthetic click bypasses outside pointer/focus dismissal, testing the dialog closing observer.
    await page.getByRole("button", { name: "Close", exact: true }).evaluate(node => node.click())
    await page.waitForFunction(() => document.querySelector("#test-options")?.inert)
    await menu.waitFor({ state: "detached" })
    await page.locator("dialog").waitFor({ state: "detached" })
    await assertClean(0)

    await load()
    await trigger.click()
    await settledOpen()
    await page.locator("dialog").evaluate(node => node.close())
    await menu.waitFor({ state: "detached" })
    await assertClean()
    await configure({ visible: false })
    await assertClean(0)

    await load()
    await trigger.click()
    await settledOpen()
    await page.keyboard.press("Escape")
    await page.keyboard.press("Escape")
    await page.locator("dialog").waitFor({ state: "detached" })
    await assertClean(0)

    // Fallback overflow is shared while one menu exits and another opens.
    await load("?multiple")
    await configure({ open: true })
    await settledOpen()
    await freezeExit(() => configure({ open: false, secondaryOpen: true }))
    await page.locator("#secondary-options").waitFor({ state: "visible" })
    await page.evaluate(() => window.exitAnimation.play())
    await menu.waitFor({ state: "detached" })
    if (fallback) assert.equal(await page.locator("dialog").evaluate(node => node.style.overflow), "visible")
    await configure({ secondaryOpen: false })
    await page.locator("#secondary-options").waitFor({ state: "detached" })
    assert.equal(await page.locator("dialog").evaluate(node => node.style.overflow), "")
    await assertClean()

    await load("?standalone")
    await trigger.click()
    await settledOpen()
    const coveredOption = page.getByRole("button", { name: "Option 3", exact: true })
    assert.equal(await coveredOption.evaluate(node => {
      const rect = node.getBoundingClientRect()
      const hit = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2)
      return hit === node || node.contains(hit)
    }), true, "glass card menus must stay above neighboring surfaces")
    if (fallback) assert.equal(await menu.evaluate(node => node.parentElement.classList.contains("app-shell")), true)
    await freezeExit(() => coveredOption.click())
    assert.equal(await trigger.innerText(), "Option 3")
    await finishExit()
    await trigger.click()
    await settledOpen()
    await freezeExit(() => page.evaluate(() => { window.destroyed = window.destroyHarness(true) }))
    await finishExit()
    await page.evaluate(() => window.destroyed)
    await assertClean(0)

    await load("?standalone")
    await trigger.click()
    await settledOpen()
    await page.evaluate(() => window.destroyHarness(false))
    await assertClean(0)
    assert.deepEqual(errors, [])
    await page.close()
    console.log(fallback ? "dropdown fallback lifecycle passed" : "dropdown native Popover lifecycle passed")
  }
} finally { await browser.close() }
