// PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs node tests/list-preferences.browser.mjs [url]
import assert from "node:assert/strict"
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright")
const browser = await chromium.launch()
try {
  const page = await browser.newPage({ viewport: { width: 390, height: 844 }, locale: "zh-CN", hasTouch: true })
  const errors = []
  page.on("pageerror", error => errors.push(error.message))
  const setupHost = (scenario = "") => {
    localStorage.setItem("keybox-lite-language", "zh")
    window.nameComparisons = 0
    const NativeCollator = Intl.Collator
    Intl.Collator = class extends NativeCollator {
      get compare() {
        const compare = super.compare
        return (a, b) => { window.nameComparisons++; return compare(a, b) }
      }
    }
    window.installTimesReads = 0
    window.installClock = 0
    const now = Date.now
    Date.now = () => now() + window.installClock
    const pkgs = ["com.zulu", "com.alpha", "com.system"]
    const backend = { backend: "tricky-store", identity: "TS", active: true, name: "Tricky Store" }
    const status = {
      supported: true, backend,
      config: { targets: [{ package_name: "com.zulu", mode: "auto" }, { package_name: "com.system", mode: "auto" }], default_policy: {}, per_app_policy: {} },
      packages: pkgs.map(package_name => ({ package_name, mode: "auto", system: package_name === "com.system", selected: package_name !== "com.alpha", installed: true })),
      system_apps: ["com.system"], auto_add_new_apps: false,
      entry: { enabled: false, module_id: "tricky_store", linked: false, has_own_webui: false }, revision: "r1",
    }
    window.testStatus = status
    window.targetReads = []
    window.metadataReads = []
    if (scenario) {
      localStorage.setItem("keybox-lite.target-list-preferences", JSON.stringify({ sort: "name", showSystem: false }))
      if (scenario === "failure") window.metadataFailures = 1
      else {
        status.packages.find(item => item.package_name === "com.zulu").installed = false
        window.testLabels = { "com.zulu": "" }
      }
      window.holdMetadata = scenario === "late"
    }
    window.ksu = {
      enableEdgeToEdge() {}, toast() {},
      getPackagesInfo(requested) {
        const names = JSON.parse(requested)
        window.metadataReads.push(names)
        if (window.metadataFailures) { window.metadataFailures--; throw Error("Temporary metadata failure") }
        return JSON.stringify(names.map(packageName => ({ packageName, appLabel: window.testLabels?.[packageName] ?? packageName.split(".")[1] })))
      },
      spawn(command, _args, _options, callback) {
        if (command.includes(" targets status") || command.includes(" targets packages")) window.targetReads.push(command)
        const installationRead = command.includes("dumpsys package")
        if (installationRead) window.installTimesReads++
        const installFailure = window.installTimesFail
        const installDump = window.installTimesDump
        const respond = () => {
          const child = window[callback]
          if (!child) return
          let out = ""
          let rc = 0
          if (command.includes("for d in")) out = "/data/adb/modules/keybox-lite"
          else if (command.includes("targets entry status")) out = JSON.stringify(status.entry)
          else if (command.includes("targets status")) out = JSON.stringify(status)
          else if (command.includes("targets packages")) out = JSON.stringify(status.packages)
          else if (command.includes("dumpsys package")) {
            if (installFailure) rc = 1
            else out = installDump || "  Package [com.alpha] (abc):\n    firstInstallTime=2026-10-28 12:30:00\n  Package [com.zulu] (abc):\n    firstInstallTime=2026-10-29 12:30:00\n  Package [com.system] (abc):\n    firstInstallTime=2026-10-30 12:30:00"
          } else if (command.includes(" info")) out = JSON.stringify({ path: "/keybox", exists: false, size: 0, modified: 0, structure_valid: false, serials: [], backend })
          else if (command.includes("settings.sh")) out = "SCHEDULE=off\nLAST_RUN=0"
          for (const line of out.split("\n")) child.stdout.emit("data", line)
          child.emit("exit", rc)
        }
        if (window.holdInitialStatus && command.includes(" targets status")) window.releaseInitialStatus = respond
        else setTimeout(respond, command.includes("dumpsys package") ? window.installTimesDelay ?? 10 : (command.includes("targets status") || command.includes("targets packages")) ? window.statusDelay ?? 10 : 10)
      },
    }
  }
  await page.addInitScript(setupHost)
  await page.addInitScript(() => { window.holdInitialStatus = !sessionStorage.getItem("initial-pull-tested") })
  const rows = async expected => {
    await page.waitForFunction(value => JSON.stringify([...document.querySelectorAll(".app-row-label")].map(node => node.textContent)) === JSON.stringify(value), expected)
  }
  await page.goto((process.argv[2] || "http://127.0.0.1:5186") + "/#/targets")
  await page.waitForFunction(() => window.targetReads.length === 1 && document.querySelector('[data-skeleton="apps"]'))
  const firstSearch = await page.locator(".target-search > div").boundingBox()
  const firstBanner = await page.locator('[data-skeleton="backend"]').boundingBox()
  const firstList = await page.locator('[data-skeleton="apps"]').boundingBox()
  const firstTouch = await page.context().newCDPSession(page)
  const firstPoint = { x: firstList.x + firstList.width / 2, y: firstList.y + 20 }
  await firstTouch.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [firstPoint] })
  for (const dy of [12, 28, 50]) await firstTouch.send("Input.dispatchTouchEvent", {
    type: "touchMove", touchPoints: [{ x: firstPoint.x, y: firstPoint.y + dy }],
  })
  await page.waitForFunction(() => document.querySelector(".target-refresh-gap")?.classList.contains("is-armed"), null, { timeout: 2000 })
  const firstButton = await page.locator(".target-refresh-gap button").boundingBox()
  const pulledList = await page.locator('[data-skeleton="apps"]').boundingBox()
  assert.ok(Math.abs((firstButton.y - firstSearch.y - firstSearch.height) -
    (pulledList.y - firstButton.y - firstButton.height)) < 1, "first pull centers the button between the search surface and applications")
  assert.equal((await page.locator(".target-search > div").boundingBox()).y, firstSearch.y, "first pull keeps the search in place")
  assert.equal((await page.locator('[data-skeleton="backend"]').boundingBox()).y, firstBanner.y, "first pull keeps the banner in place")
  await firstTouch.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] })
  await firstTouch.detach()
  await page.evaluate(() => {
    sessionStorage.setItem("initial-pull-tested", "1")
    window.holdInitialStatus = false
    window.releaseInitialStatus()
  })
  await rows(["alpha", "zulu"])
  await page.waitForFunction(() => window.installTimesReads === 1 && document.querySelector(".target-page").getAttribute("aria-busy") === "false")
  assert.deepEqual(await page.evaluate(() => window.targetReads.map(command => command.slice(command.indexOf(" targets ") + 1).split("\n")[0])),
    ["targets status", "targets packages"], "a pull during the initial read refreshes packages after status completes")
  assert.equal(await page.locator("select").count(), 0)
  const comparisons = await page.evaluate(() => window.nameComparisons)
  assert.ok(comparisons > 0, "the spy observes real production name comparisons")
  const search = page.getByRole("searchbox", { name: "搜索包名 / UID", exact: true })
  await search.fill("ALPHA")
  await rows(["alpha"])
  await search.fill("")
  await rows(["alpha", "zulu"])
  const alphaToggle = page.locator('[data-virtual-key="com.alpha"] input[type="checkbox"]')
  await alphaToggle.click()
  await rows(["alpha", "zulu"])
  await alphaToggle.click()
  await rows(["alpha", "zulu"])
  assert.equal(await page.evaluate(() => window.nameComparisons), comparisons, "query and selection do not re-run the ordering comparator")
  await page.locator("#targets-menu-trigger").click()
  const system = page.getByRole("menuitemcheckbox", { name: "显示系统应用", exact: true })
  const refresh = page.getByRole("menuitem", { name: "刷新", exact: true })
  const sortTrigger = page.getByRole("button", { name: "排序", exact: true })
  const tracked = page.getByRole("menuitem", { name: "选择系统应用", exact: true })
  const menuItems = await page.locator("#targets-menu > button, #targets-menu > [role=group] button").evaluateAll(nodes => nodes.map(node => node.getAttribute("aria-label") || node.textContent.trim()))
  assert.deepEqual(menuItems, ["全选可见应用", "取消选中可见应用", "刷新", "排序", "显示系统应用", "添加包名 / UID", "选择系统应用", "自动添加新安装的用户应用", "全局补丁策略", "WebUI 入口", "导入配置", "导出配置"])
  assert.equal(await page.locator("#targets-menu > [role=separator]").count(), 3)
  const bounds = await Promise.all([refresh, sortTrigger, system, tracked].map(item => item.boundingBox()))
  assert.ok(bounds[0].y < bounds[1].y && bounds[1].y < bounds[2].y && bounds[2].y < bounds[3].y, "display preferences form the group immediately after refresh")
  for (const box of bounds.slice(1)) {
    assert.ok(Math.abs(box.x - bounds[0].x) < 1 && Math.abs(box.width - bounds[0].width) < 1, "menu rows share alignment and width")
  }
  await system.click()
  await rows(["alpha", "system", "zulu"])
  assert.equal(await system.getAttribute("aria-checked"), "true")
  await page.mouse.move(0, 0)
  await page.waitForTimeout(180)
  assert.equal(await system.evaluate(node => getComputedStyle(node).backgroundColor), "rgba(0, 0, 0, 0)", "a checkmark does not add a persistent row background")
  await system.click()
  await rows(["alpha", "zulu"])
  assert.equal(await system.getAttribute("aria-checked"), "false")
  const sort = async label => {
    await page.getByRole("button", { name: "排序", exact: true }).click()
    await page.mouse.move(0, 0)
    assert.equal(await page.locator(".dropdown-menu button[aria-pressed=true]").evaluate(node => getComputedStyle(node).backgroundColor), "rgba(0, 0, 0, 0)", "selected sort options use their checkmark without a persistent background")
    await page.getByRole("button", { name: label, exact: true }).click()
  }
  await sort("最近安装")
  await rows(["zulu", "alpha"])
  assert.equal(await page.getByRole("menu").count(), 1)
  await sort("应用名称 A–Z")
  await rows(["alpha", "zulu"])
  await sort("最近安装")
  await rows(["zulu", "alpha"])
  assert.equal(await page.evaluate(() => window.installTimesReads), 1, "repeated installed/name switches reuse the cache")
  await system.click()
  await rows(["system", "zulu", "alpha"])
  await page.evaluate(() => { location.hash = "#/" })
  await page.locator(".target-page").waitFor({ state: "detached" })
  await page.evaluate(() => { location.hash = "#/targets" })
  await rows(["system", "zulu", "alpha"])
  assert.equal(await page.evaluate(() => window.installTimesReads), 1, "returning to the page reuses fresh dates")
  await page.reload()
  await rows(["system", "zulu", "alpha"])
  await page.locator("#targets-menu-trigger").click()
  assert.equal(await system.getAttribute("aria-checked"), "true")
  for (const width of [320, 390, 640]) {
    await page.setViewportSize({ width, height: 844 })
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
  }
  await page.setViewportSize({ width: 390, height: 844 })
  if (!await sortTrigger.count()) await page.locator("#targets-menu-trigger").click()
  await page.getByRole("button", { name: "排序", exact: true }).click()
  await page.keyboard.press("Escape")
  assert.equal(await page.getByRole("menu").count(), 1, "Escape closes the sort dropdown before the parent menu")
  await page.keyboard.press("Escape")
  await page.getByRole("menu").waitFor({ state: "detached" })
  await page.evaluate(() => {
    window.installTimesDump = "Package [com.alpha]\n  firstInstallTime=2026-11-01 12:30:00\nPackage [com.zulu]\n  firstInstallTime=2026-10-29 12:30:00\nPackage [com.system]\n  firstInstallTime=2026-10-30 12:30:00"
  })
  const beforeRefresh = await page.evaluate(() => window.installTimesReads)
  const searchBeforePull = await page.locator(".target-search > div").boundingBox()
  const rowForPull = await page.locator("[data-virtual-key]").first().boundingBox()
  const touch = await page.context().newCDPSession(page)
  const point = { x: rowForPull.x + rowForPull.width / 2, y: rowForPull.y + 20 }
  await touch.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [point] })
  for (const dy of [12, 28, 50]) await touch.send("Input.dispatchTouchEvent", {
    type: "touchMove", touchPoints: [{ x: point.x, y: point.y + dy }],
  })
  await page.waitForFunction(() => document.querySelector(".target-refresh-gap").clientHeight >= 32)
  const gap = await page.locator(".target-refresh-gap").boundingBox()
  assert.ok(Math.abs(gap.y - searchBeforePull.y - searchBeforePull.height) < 1, "refresh opens at the visible search edge above applications")
  const refreshButton = await page.locator(".target-refresh-gap button").boundingBox()
  assert.ok(Math.abs((refreshButton.y - gap.y) - (gap.y + gap.height - refreshButton.y - refreshButton.height)) < 1,
    "refresh has equal space above and below the button")
  assert.ok(Math.abs((await page.locator(".target-search > div").boundingBox()).y - searchBeforePull.y) < 1, "pulling leaves the search bar and banner in place")
  await touch.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] })
  await touch.detach()
  await rows(["alpha", "system", "zulu"])
  assert.equal(await page.evaluate(() => window.installTimesReads), beforeRefresh + 1, "explicit refresh rereads unchanged package names inside the TTL")

  await page.locator("#targets-menu-trigger").click()
  await sort("应用名称 A–Z")
  await sort("最近安装")
  assert.equal(await page.evaluate(() => window.installTimesReads), beforeRefresh + 1)
  await page.evaluate(() => { window.installClock += 30_000 })
  await sort("应用名称 A–Z")
  await sort("最近安装")
  assert.equal(await page.evaluate(() => window.installTimesReads), beforeRefresh + 1, "sorting keeps the current snapshot until an explicit application refresh")
  await rows(["alpha", "system", "zulu"])
  await page.keyboard.press("Escape")
  await page.getByRole("menu").waitFor({ state: "detached" })
  await page.evaluate(() => { window.installTimesFail = true; window.installClock += 30_000 })
  await page.locator("#targets-menu-trigger").click()
  await sort("应用名称 A–Z")
  await rows(["alpha", "system", "zulu"])
  await sort("最近安装")
  assert.equal(await page.evaluate(() => window.installTimesReads), beforeRefresh + 1)
  await refresh.click()
  await page.getByRole("alert").filter({ hasText: "无法读取安装时间" }).waitFor()
  await rows(["alpha", "system", "zulu"])
  await page.addInitScript(() => {
    window.testLabels = { "com.alpha": "Zulu", "com.zulu": "Alpha" }
    window.installTimesDelay = 400
    localStorage.setItem("keybox-lite.target-list-preferences", JSON.stringify({ sort: "package", showSystem: false }))
  })
  await page.reload()
  await rows(["Zulu", "Alpha"])
  await page.locator("#targets-menu-trigger").click()
  const movingRow = page.locator('[data-virtual-key="com.zulu"]')
  const oldTop = (await movingRow.boundingBox()).y
  await sort("最近安装")
  await page.waitForTimeout(120)
  await rows(["Zulu", "Alpha"])
  assert.ok((await sortTrigger.innerText()).includes("包名"), "selection stays on the applied sort while dates load")
  await page.waitForFunction(() => document.querySelector('button[aria-label="排序"]').textContent.includes("最近安装"))
  await page.waitForTimeout(45)
  const middleTop = (await movingRow.boundingBox()).y
  await rows(["Alpha", "Zulu"])
  await page.waitForTimeout(300)
  const finalTop = (await movingRow.boundingBox()).y
  assert.ok(middleTop < oldTop - 1 && middleTop > finalTop + 1, "ready install order moves rows smoothly")

  await sort("包名")
  await rows(["Zulu", "Alpha"])
  await page.evaluate(() => { window.installClock += 30_000 })
  await sort("最近安装")
  await page.waitForTimeout(60)
  await sort("应用名称 A–Z")
  await rows(["Alpha", "Zulu"])
  await page.waitForTimeout(450)
  assert.ok((await sortTrigger.innerText()).includes("A–Z"), "late installation response cannot replace a newer choice")
  const completedReads = await page.evaluate(() => window.installTimesReads)
  await sort("最近安装")
  await rows(["Alpha", "Zulu"])
  assert.equal(await page.evaluate(() => window.installTimesReads), completedReads, "a cancelled subscriber leaves the completed shared read available to the next choice")

  await page.addInitScript(() => {
    localStorage.setItem("keybox-lite.target-list-preferences", JSON.stringify({ sort: "installed", showSystem: false }))
  })
  await page.reload()
  await page.getByRole("searchbox", { name: "搜索包名 / UID", exact: true }).waitFor()
  await page.waitForTimeout(100)
  assert.equal(await page.locator(".app-row").count(), 0, "restored recent sorting never exposes a default-order list")
  await rows(["Alpha", "Zulu"])
  const catalogReads = await page.evaluate(() => [window.metadataReads.length, window.installTimesReads])
  await page.evaluate(() => {
    window.testStatus.config.targets = [{ package_name: "com.alpha", mode: "auto" }]
    window.testStatus.revision = "external-targets"
    dispatchEvent(new Event("focus"))
  })
  await page.waitForFunction(() => document.querySelector('[data-virtual-key="com.alpha"] input[type="checkbox"]')?.checked &&
    !document.querySelector('[data-virtual-key="com.zulu"] input[type="checkbox"]')?.checked)
  assert.deepEqual(await page.evaluate(() => [window.metadataReads.length, window.installTimesReads]), catalogReads,
    "automatic target reads preserve application metadata and installation dates")
  assert.deepEqual(errors, [])
  await page.close()

  for (const scenario of ["missing", "failure", "late"]) {
    const metadataPage = await browser.newPage({ viewport: { width: 390, height: 844 }, locale: "zh-CN" })
    const metadataErrors = []
    metadataPage.on("pageerror", error => metadataErrors.push(error.message))
    await metadataPage.addInitScript(setupHost, scenario)
    if (scenario === "late") {
      // Delay delivery at the component boundary while the real apps.ts still
      // handles native labels, caching and batching.
      await metadataPage.route(/\/src\/lib\/apps\.ts(?:\?.*)?$/, route => {
        if (new URL(route.request().url()).searchParams.has("metadata-original")) return route.continue()
        return route.fulfill({ contentType: "text/javascript", body: `
          import { packagesInfo as read } from "/src/lib/apps.ts?metadata-original=1";
          export async function packagesInfo(names, signal, onBatch) {
            const held = window.holdMetadata, batches = [];
            const result = await read(names, signal, value => {
              if (held) batches.push(value);
              else onBatch?.(value);
            });
            if (!held) return result;
            return new Promise(resolve => {
              window.releaseMetadata = () => {
                for (const value of batches) onBatch?.(value);
                resolve(result);
              };
              window.metadataHeld = true;
            });
          }
        ` })
      })
    }
    const metadataRows = expected => metadataPage.waitForFunction(value =>
      JSON.stringify([...document.querySelectorAll(".app-row-label")].map(node => node.textContent)) === JSON.stringify(value),
    expected, { timeout: 5000 })
    await metadataPage.goto((process.argv[2] || "http://127.0.0.1:5186") + "/#/targets")
    await metadataRows(scenario === "missing" ? ["alpha", "com.zulu"] : ["com.alpha", "com.zulu"])
    await metadataPage.waitForFunction(() => window.metadataReads.length === 1 && (!window.holdMetadata || window.metadataHeld))
    const missingRow = metadataPage.locator('[data-virtual-key="com.zulu"]')
    if (scenario !== "failure") assert.ok((await missingRow.innerText()).includes("未安装"))
    const namesBefore = await metadataPage.evaluate(() => window.testStatus.packages.map(item => item.package_name))
    const readsBefore = await metadataPage.evaluate(() => window.metadataReads.length)
    await metadataPage.evaluate(() => {
      window.testStatus.packages.find(item => item.package_name === "com.zulu").installed = true
      window.testStatus.revision = "r2"
      window.testLabels = { "com.zulu": "Aardvark" }
      window.holdMetadata = false
      window.statusDelay = 160
    })
    await metadataPage.locator("#targets-menu-trigger").click()
    await metadataPage.getByRole("menuitem", { name: "刷新", exact: true }).click()
    await metadataPage.waitForTimeout(60)
    assert.equal(await metadataPage.evaluate(() => window.metadataReads.length), readsBefore, "metadata reload waits for the fresh status")
    await metadataRows(["Aardvark", "alpha"])
    assert.equal((await missingRow.innerText()).includes("未安装"), false)
    assert.deepEqual(await metadataPage.evaluate(() => window.testStatus.packages.map(item => item.package_name)), namesBefore)
    assert.equal(await metadataPage.evaluate(() => window.metadataReads.length), readsBefore + 1, scenario)
    assert.deepEqual(await metadataPage.evaluate(() => window.metadataReads.at(-1)),
      scenario === "failure" ? ["com.alpha", "com.system", "com.zulu"] : ["com.zulu"],
      "valid labels retain their 30-second cache while missing or failed labels retry")

    if (scenario === "late") {
      await metadataPage.evaluate(() => window.releaseMetadata())
      await metadataPage.waitForTimeout(100)
      await metadataRows(["Aardvark", "alpha"])
    }
    const metadataSearch = metadataPage.getByRole("searchbox", { name: "搜索包名 / UID", exact: true })
    const stable = await metadataPage.evaluate(() => [window.metadataReads.length, window.nameComparisons])
    await metadataSearch.fill("aardvark")
    await metadataRows(["Aardvark"])
    await metadataSearch.fill("")
    await metadataRows(["Aardvark", "alpha"])
    const toggle = metadataPage.locator('[data-virtual-key="com.alpha"] input[type="checkbox"]')
    await toggle.click()
    await toggle.click()
    await metadataPage.locator("#targets-menu-trigger").click()
    await metadataPage.getByRole("menuitem", { name: "WebUI 入口", exact: true }).click()
    await metadataPage.getByText("未注入", { exact: true }).waitFor()
    assert.deepEqual(await metadataPage.evaluate(() => [window.metadataReads.length, window.nameComparisons]), stable,
      "search, selection and standalone entry reads neither reload metadata nor repeat ordering comparisons")
    assert.deepEqual(metadataErrors, [])
    await metadataPage.close()
  }
  console.log("PASS: first-entry touch refresh, balanced refresh spacing, metadata retries, missing installs, late callbacks, stable comparator counts, TTL/cache reuse, forced refresh, sort motion, cancellation and restored preferences")
} finally {
  await browser.close()
}
