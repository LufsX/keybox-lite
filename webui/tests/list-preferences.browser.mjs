// PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs node tests/list-preferences.browser.mjs [url]
import assert from "node:assert/strict"
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright")
const browser = await chromium.launch()
try {
  const page = await browser.newPage({ viewport: { width: 390, height: 844 }, locale: "zh-CN" })
  const errors = []
  page.on("pageerror", error => errors.push(error.message))
  await page.addInitScript(() => {
    localStorage.setItem("keybox-lite-language", "zh")
    const pkgs = ["com.zulu", "com.alpha", "com.system"]
    const backend = { backend: "tricky-store", identity: "TS", active: true, name: "Tricky Store" }
    const status = {
      supported: true, backend,
      config: { targets: [{ package_name: "com.zulu", mode: "auto" }, { package_name: "com.system", mode: "auto" }], default_policy: {}, per_app_policy: {} },
      packages: pkgs.map(package_name => ({ package_name, mode: "auto", system: package_name === "com.system", selected: package_name !== "com.alpha", installed: true })),
      system_apps: ["com.system"], auto_add_new_apps: false,
      entry: { enabled: false, module_id: "tricky_store", linked: false, has_own_webui: false }, revision: "r1",
    }
    window.ksu = {
      enableEdgeToEdge() {}, toast() {},
      getPackagesInfo() { return JSON.stringify(pkgs.map(packageName => ({ packageName, appLabel: window.testLabels?.[packageName] || packageName.split(".")[1] }))) },
      spawn(command, _args, _options, callback) {
        setTimeout(() => {
          const child = window[callback]
          if (!child) return
          let out = ""
          let rc = 0
          if (command.includes("for d in")) out = "/data/adb/modules/keybox-lite"
          else if (command.includes("targets status")) out = JSON.stringify(status)
          else if (command.includes("dumpsys package")) {
            if (window.installTimesFail) rc = 1
            else out = "  Package [com.alpha] (abc):\n    firstInstallTime=2026-10-28 12:30:00\n  Package [com.zulu] (abc):\n    firstInstallTime=2026-10-29 12:30:00\n  Package [com.system] (abc):\n    firstInstallTime=2026-10-30 12:30:00"
          } else if (command.includes(" info")) out = JSON.stringify({ path: "/keybox", exists: false, size: 0, modified: 0, structure_valid: false, serials: [], backend })
          else if (command.includes("settings.sh")) out = "SCHEDULE=off\nLAST_RUN=0"
          for (const line of out.split("\n")) child.stdout.emit("data", line)
          child.emit("exit", rc)
        }, command.includes("dumpsys package") ? window.installTimesDelay ?? 10 : 10)
      },
    }
  })
  const rows = async expected => {
    await page.waitForFunction(value => JSON.stringify([...document.querySelectorAll(".app-row-label")].map(node => node.textContent)) === JSON.stringify(value), expected)
  }
  await page.goto((process.argv[2] || "http://127.0.0.1:5186") + "/#/targets")
  await rows(["alpha", "zulu"])
  assert.equal(await page.locator("select").count(), 0)
  await page.locator("#targets-menu-trigger").click()
  const system = page.getByRole("menuitemcheckbox", { name: "显示系统应用", exact: true })
  const refresh = page.getByRole("menuitem", { name: "刷新", exact: true })
  const sortTrigger = page.getByRole("button", { name: "排序", exact: true })
  const tracked = page.getByRole("menuitem", { name: "选择系统应用", exact: true })
  const menuItems = await page.locator("#targets-menu > button, #targets-menu > [role=group] button").evaluateAll(nodes => nodes.map(node => node.getAttribute("aria-label") || node.textContent.trim()))
  assert.deepEqual(menuItems, ["全选可见应用", "取消选中可见应用", "刷新", "排序", "显示系统应用", "添加包名 / UID", "选择系统应用", "自动添加新安装的用户应用", "全局补丁策略", "WebUI 入口"])
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
  await system.click()
  await rows(["system", "zulu", "alpha"])
  await page.reload()
  await rows(["system", "zulu", "alpha"])
  await page.locator("#targets-menu-trigger").click()
  assert.equal(await system.getAttribute("aria-checked"), "true")
  for (const width of [320, 390, 640]) {
    await page.setViewportSize({ width, height: 844 })
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
  }
  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole("button", { name: "排序", exact: true }).click()
  await page.keyboard.press("Escape")
  assert.equal(await page.getByRole("menu").count(), 1, "Escape closes the sort dropdown before the parent menu")
  await page.keyboard.press("Escape")
  await page.getByRole("menu").waitFor({ state: "detached" })
  await page.evaluate(() => { window.installTimesFail = true })
  await page.locator("#targets-menu-trigger").click()
  await sort("应用名称 A–Z")
  await rows(["alpha", "system", "zulu"])
  await sort("最近安装")
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
  await sort("最近安装")
  await page.waitForTimeout(60)
  await sort("应用名称 A–Z")
  await rows(["Alpha", "Zulu"])
  await page.waitForTimeout(450)
  assert.ok((await sortTrigger.innerText()).includes("A–Z"), "late installation response cannot replace a newer choice")

  await page.addInitScript(() => {
    localStorage.setItem("keybox-lite.target-list-preferences", JSON.stringify({ sort: "installed", showSystem: false }))
  })
  await page.reload()
  await page.getByRole("searchbox", { name: "搜索包名 / UID", exact: true }).waitFor()
  await page.waitForTimeout(100)
  assert.equal(await page.locator(".app-row").count(), 0, "restored recent sorting never exposes a default-order list")
  await rows(["Alpha", "Zulu"])
  assert.deepEqual(errors, [])
  console.log("PASS: menu grouping, filtering, prepared installation order, sort motion, cancellation, restored preferences and narrow viewports")
} finally {
  await browser.close()
}
