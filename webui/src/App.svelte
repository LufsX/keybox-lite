<script lang="ts">
  import { onMount, tick } from "svelte"
  import { pageExit } from "./lib/motion"
  import { fade, slide } from "svelte/transition"
  import {
    RefreshCw, Loader2, ShieldCheck, ShieldAlert, ShieldQuestion,
    Languages, HardDrive, Download, ArrowLeft, ChevronDown, ChevronRight, EllipsisVertical,
    CalendarClock, History, Monitor, Sun, Moon, Square, ListChecks
  } from "lucide-svelte"
  import { exec, execUpdate, toast, enableEdgeToEdge, type ExecResult } from "./lib/bridge"
  import { messages, detect, isLocale, supportedLanguages, type Locale } from "./lib/i18n"
  import { LogBuffer, stripAnsi, type LogLine } from "./lib/log"
  import Dropdown from "./lib/components/Dropdown.svelte"
  import LogOutput from "./lib/components/LogOutput.svelte"
  import { ui } from "./lib/ui"
  import CertificateDetails from "./lib/components/CertificateDetails.svelte"
  import TargetManager from "./lib/components/TargetManager.svelte"
  import Toast from "./lib/components/Toast.svelte"
  import { parseCertificateStatus, certificateStatusForSerial, type CertificateVerification, type CertificateValidity, type ValidityStatus } from "./lib/certificateStatus"

  // Hash paths also work when the static WebUI is opened directly or refreshed.
  let targetsPage = $state(typeof window !== "undefined" && window.location.hash === "#/targets")
  let targetManager: { canLeave(): boolean; toggleMenu(): void } | undefined = $state()
  let targetMenuOpen = $state(false)
  let targetTitleVisible = $state(false)
  let targetTitle = $state("Tricky Store")
  function syncRoute() {
    const next = window.location.hash === "#/targets"
    if (targetsPage && !next && targetManager && !targetManager.canLeave()) {
      // Restore a history entry so Back can return home after the guard clears.
      window.location.hash = "/targets"
      return
    }
    if (targetsPage !== next) {
      targetsPage = next
      void tick().then(() => window.scrollTo({ top: 0, behavior: "instant" }))
    }
    themeOpen = false
    languageOpen = false
    sourceOpen = false
  }
  function returnHome(event: MouseEvent) {
    event.preventDefault()
    if (targetManager && !targetManager.canLeave()) return
    targetsPage = false
    window.location.hash = "/"
    void tick().then(() => window.scrollTo({ top: 0, behavior: "instant" }))
  }

  const MODULE_ID = "keybox-lite"
  const badge = "inline-flex shrink-0 items-center justify-center gap-1.5 whitespace-nowrap rounded-[7px] px-1.5 py-0.5 text-[11px] font-[550] leading-snug"
  const statusValue = "flex min-h-[18px] items-center gap-1.5 text-xs font-semibold"

  function savedLanguage(): Locale {
    try {
      const value = localStorage.getItem("keybox-lite-language")
      if (isLocale(value)) return value
    } catch { /* Storage can be disabled by the host. */ }
    return detect()
  }
  let lang = $state<Locale>(savedLanguage())
  const m = $derived(messages[lang])

  type Theme = "system" | "light" | "dark"
  const themes: Theme[] = ["system", "light", "dark"]
  let themeOpen = $state(false)
  let languageOpen = $state(false)
  const themeOptions = $derived(themes.map(value => ({
    value, label: value === "system" ? m.themeSystem : value === "light" ? m.themeLight : m.themeDark,
  })))
  const languageOptions = supportedLanguages.map(({ code, nativeLabel }) => ({ value: code, label: nativeLabel, lang: code }))
  function toggleTheme(open = !themeOpen) {
    themeOpen = open
    if (open) {
      languageOpen = false
      sourceOpen = false
    }
  }
  function toggleLanguage(open = !languageOpen) {
    languageOpen = open
    if (open) {
      themeOpen = false
      sourceOpen = false
    }
  }
  function setLanguage(value: Locale) {
    lang = value
    languageOpen = false
    try { localStorage.setItem("keybox-lite-language", value) } catch { /* Keep the in-session choice. */ }
  }
  function savedTheme(): Theme {
    try {
      const value = localStorage.getItem("keybox-lite-theme")
      if (value === "light" || value === "dark") return value
    } catch { /* Storage can be disabled by the host. */ }
    return "system"
  }
  let theme = $state<Theme>(savedTheme())
  let systemDark = $state(typeof matchMedia !== "undefined" && matchMedia("(prefers-color-scheme: dark)").matches)
  const dark = $derived(theme === "dark" || (theme === "system" && systemDark))
  $effect(() => {
    document.documentElement.classList.toggle("dark", dark)
    document.documentElement.style.colorScheme = dark ? "dark" : "light"
  })
  function setTheme(value: Theme) {
    theme = value
    themeOpen = false
    try { localStorage.setItem("keybox-lite-theme", value) } catch { /* Keep the in-session choice. */ }
  }

  // Downloader attempts have bounded waits; updates also have a 20-minute supervisor deadline.
  // Update can try multiple catalog candidates and three sources; this is a UI
  // waiting budget, not a total backend deadline. Neither timeout cancels the job.
  const VERIFY_TIMEOUT = 30 * 60_000
  const UPDATE_TIMEOUT = 75 * 60_000
  type Busy = "update" | "check" | "refresh" | "settings" | "log"
  type Schedule = "off" | "daily" | "weekly"
  type KeyboxSource = "auto" | "meow" | "spectre"
  const keyboxSources: KeyboxSource[] = ["auto", "meow", "spectre"]
  let keyboxSource = $state<KeyboxSource>("auto")
  let skipRevocation = $state(false)
  async function setSkipRevocation(value: boolean) {
    if (busy !== null || !moddir) return
    await run("settings", async () => {
      const r = await exec('sh "' + moddir + '/settings.sh" skip-revocation ' + (value ? "1" : "0"))
      requireSuccess(r, m.settingsFailed)
      await loadMeta()
      if (skipRevocation !== value) throw new Error(m.settingsFailed)
    })
  }
  let sourceOpen = $state(false)
  const sourceOptions = $derived(keyboxSources.map(value => ({
    value, label: value === "auto" ? m.sourceAuto : value === "meow" ? "Meow" : "Spectre",
  })))
  function toggleSource(open: boolean) {
    sourceOpen = open
    if (open) {
      themeOpen = false
      languageOpen = false
    }
  }
  async function setSource(value: string) {
    if (busy !== null || !moddir || !keyboxSources.includes(value as KeyboxSource)) return
    await run("settings", async () => {
      const r = await exec('sh "' + moddir + '/settings.sh" source ' + value)
      requireSuccess(r, m.settingsFailed)
      await loadMeta()
      if (keyboxSource !== value) throw new Error(m.settingsFailed)
    })
  }
  interface KeyboxInfo {
    path: string
    exists: boolean
    size: number
    modified: number
    structure_valid: boolean
    serials: string[]
    validity?: { status: ValidityStatus; certificates: CertificateValidity[] }
    integrity?: import("./lib/certificateStatus").KeyboxIntegrity
    backend?: { identity: string; active: boolean } | null
    backend_error?: string
  }

  let initialDone = $state(false)
  let infoStale = $state(false)
  let detailsOpen = $state(false)
  let reducedMotion = $state(false)
  let moddir = $state("")
  let info = $state<KeyboxInfo | null>(null)
  let output = $state("")
  let savedLogVisible = $state(false)
  let error = $state("")
  let checkRc = $state<number | null>(null)
  let certificateVerification = $state<CertificateVerification | null>(null)

  function clearCertificateCheck() {
    checkRc = null
    certificateVerification = null
  }
  let busy = $state<Busy | null>(null)
  let cancelling = $state(false)
  let cancelRequested = $state(false)
  let updateRunning = $state(false)
  let updateMonitor: AbortController | undefined
  let updateStop: (() => Promise<ExecResult>) | undefined
  let stopPending: Promise<void> | undefined
  let outputTimer: ReturnType<typeof setTimeout> | undefined
  let pendingOutput = ""
  let disposed = false
  const OUTPUT_LIMIT = 80_000
  const logBuffer = new LogBuffer(OUTPUT_LIMIT)
  let outputLines = $state.raw<LogLine[]>([])

  function setOutput(text: string, append = false) {
    if (append) logBuffer.append(text)
    else logBuffer.replace(text)
    output = logBuffer.text
    outputLines = logBuffer.lines.slice()
  }

  const schedules: Schedule[] = ["off", "daily", "weekly"]
  let schedule = $state<Schedule | null>(null)
  let scheduleChoice = $state<Schedule | null>(null)
  const displayedSchedule = $derived(scheduleChoice ?? schedule)
  let lastRun = $state(0)
  let version = $state("")
  let records = $state<string[]>([])

  const bin = $derived(moddir ? moddir + "/bin/keyboxlite" : "")
  const structureOk = $derived(info?.structure_valid === true)

  function validityLabel(status: ValidityStatus = "unknown") {
    return status === "valid" ? m.timeValid : status === "expired" ? m.expired : status === "not_yet_valid" ? m.notYetValid : m.validityUnknown
  }

  function fmtSize(n: number) {
    if (!n) return "0 B"
    return n > 1024 ? (n / 1024).toFixed(1) + " KB" : n + " B"
  }
  function fmtTime(n: number) {
    return n ? new Date(n * 1000).toLocaleString() : "--"
  }
  function kv(text: string, key: string): string {
    for (const line of text.split("\n")) {
      const i = line.indexOf("=")
      if (i > 0 && line.slice(0, i).trim() === key) return line.slice(i + 1).trim()
    }
    return ""
  }

  function failure(r: ExecResult, fallback: string): string {
    if (r.failure === "unavailable") return m.bridgeUnavailable
    if (r.failure === "timeout") return m.commandTimeout
    return r.stderr.trim() || r.stdout.trim() || fallback
  }

  function requireSuccess(r: ExecResult, fallback: string) {
    if (r.errno !== 0) throw new Error(failure(r, fallback))
  }

  async function run(kind: Busy, task: () => Promise<void>) {
    if (busy !== null) return
    busy = kind
    sourceOpen = false
    error = ""
    try {
      await task()
    } catch (e) {
      error = [error, e instanceof Error ? e.message : String(e)].filter(Boolean).join("\n")
    } finally {
      if (kind === "refresh") initialDone = true
      busy = null
    }
  }

  async function resolveDir() {
    const r = await exec(
      'for d in /data/adb/modules/' + MODULE_ID + ' /data/adb/modules_update/' + MODULE_ID +
      '; do if [ -d "$d" ]; then printf \'%s\\n\' "$d"; exit 0; fi; done; exit 1'
    )
    requireSuccess(r, m.moduleMissing)
    const dir = r.stdout.trim()
    if (dir !== "/data/adb/modules/" + MODULE_ID && dir !== "/data/adb/modules_update/" + MODULE_ID) {
      throw new Error(m.moduleMissing)
    }
    moddir = dir
  }

  function keyboxIdentity(value: KeyboxInfo | null): string {
    return value ? JSON.stringify([value.path, value.exists, value.size, value.modified, value.serials, value.validity?.certificates, value.integrity]) : ""
  }

  async function readInfo(): Promise<KeyboxInfo> {
    const r = await exec('"' + bin + '" info')
    requireSuccess(r, m.infoFailed)
    const value: unknown = JSON.parse(r.stdout)
    if (typeof value !== "object" || value === null) throw new Error(m.invalidInfo)
    const v = value as Record<string, unknown>
    if (
      typeof v.path !== "string" || typeof v.exists !== "boolean" ||
      typeof v.size !== "number" || !Number.isFinite(v.size) || v.size < 0 ||
      typeof v.modified !== "number" || !Number.isFinite(v.modified) ||
      typeof v.structure_valid !== "boolean" || !Array.isArray(v.serials) ||
      !v.serials.every((s: unknown) => typeof s === "string")
    ) throw new Error(m.invalidInfo)
    if (v.validity !== undefined) {
      const validity = v.validity as KeyboxInfo["validity"]
      const states = ["valid", "expired", "not_yet_valid", "unknown"]
      if (!validity || !states.includes(validity.status) || !Array.isArray(validity.certificates) ||
        !validity.certificates.every(c => c && typeof c.serial === "string" &&
          Number.isFinite(c.not_before) && Number.isFinite(c.not_after) && states.includes(c.status))) {
        throw new Error(m.invalidInfo)
      }
    }
    if (v.integrity !== undefined) {
      const integrity = v.integrity as KeyboxInfo["integrity"]
      if (!integrity || !["valid", "invalid", "unknown"].includes(integrity.status) ||
        !Array.isArray(integrity.keys) || !integrity.keys.every(k => k &&
          ["ec", "rsa"].includes(k.algorithm) && typeof k.private_key_valid === "boolean" &&
          typeof k.key_match === "boolean" && typeof k.chain_valid === "boolean" &&
          ["google", "aosp_ec", "aosp_rsa", "knox", "unknown"].includes(k.root))) {
        throw new Error(m.invalidInfo)
      }
    }
    if (v.backend != null) {
      const backend = v.backend as Record<string, unknown>
      if (typeof backend !== "object" || typeof backend.identity !== "string" || typeof backend.active !== "boolean") throw new Error(m.invalidInfo)
    }
    if (v.backend_error !== undefined && typeof v.backend_error !== "string") throw new Error(m.invalidInfo)
    return v as unknown as KeyboxInfo
  }

  let infoSequence = 0
  async function loadInfo(): Promise<KeyboxInfo | null> {
    const sequence = ++infoSequence
    try {
      const nextInfo = await readInfo()
      if (disposed || sequence !== infoSequence) return null
      if (keyboxIdentity(info) !== keyboxIdentity(nextInfo)) clearCertificateCheck()
      info = nextInfo
      infoStale = false
      return nextInfo
    } catch (e) {
      if (!disposed && sequence === infoSequence) {
        infoStale = true
        clearCertificateCheck()
      }
      throw e
    }
  }

  async function loadMeta() {
    const s = await exec('sh "' + moddir + '/settings.sh" get')
    requireSuccess(s, m.settingsFailed)
    const next = kv(s.stdout, "SCHEDULE")
    const last = Number(kv(s.stdout, "LAST_RUN"))
    const src = kv(s.stdout, "SOURCE") || "auto"
    const skip = kv(s.stdout, "SKIP_REVOCATION") || "0"
    if (!schedules.includes(next as Schedule) || !Number.isFinite(last) || last < 0 || !keyboxSources.includes(src as KeyboxSource) || !["0", "1"].includes(skip)) {
      throw new Error(m.settingsFailed)
    }
    schedule = next as Schedule
    lastRun = last
    keyboxSource = src as KeyboxSource
    skipRevocation = skip === "1"
    const rec = await exec(
      'grep \'^version=\' "' + moddir + '/module.prop" || exit $?; echo \'---\'; ' +
      'if [ -f /data/local/tmp/keybox-update.history ]; then tail -n 20 /data/local/tmp/keybox-update.history; fi'
    )
    requireSuccess(rec, m.recordsFailed)
    const parts = rec.stdout.split("\n---\n")
    version = kv(parts[0] || "", "version")
    records = (parts[1] || "")
      .split("\n")
      .map((x) => x.trim())
      .filter(Boolean)
      .reverse()
  }

  async function reload() {
    const errors: string[] = []
    for (const load of [loadInfo, loadMeta]) {
      try {
        await load()
      } catch (e) {
        errors.push(e instanceof Error ? e.message : String(e))
      }
    }
    if (errors.length) throw new Error(errors.join("\n"))
  }

  async function refresh() {
    await run("refresh", async () => {
      if (!moddir) await resolveDir()
      await reload()
    })
  }

  function flushOutput() {
    clearTimeout(outputTimer)
    outputTimer = undefined
    if (!disposed && pendingOutput) setOutput(pendingOutput, true)
    pendingOutput = ""
  }

  function appendOutput(line: string) {
    if (disposed) return
    pendingOutput = (pendingOutput + stripAnsi(line + "\n")).slice(-OUTPUT_LIMIT)
    outputTimer ??= setTimeout(flushOutput, 50)
  }

  async function update() {
    if (!moddir) return
    await run("update", async () => {
      savedLogVisible = false
      setOutput("")
      clearCertificateCheck()
      cancelRequested = false
      updateMonitor = new AbortController()
      try {
        const r = await execUpdate('"' + bin + '" run-update "' + moddir + '/action.sh" /data/adb/keybox-lite', (text) => {
          if (!disposed) setOutput(stripAnsi(text))
        }, UPDATE_TIMEOUT, updateMonitor.signal, (stop) => {
          updateStop = stop
          updateRunning = !disposed
        })
        updateRunning = false
        await stopPending
        if (disposed) return
        flushOutput()
        const stopped = !r.failure && r.errno === 130 && cancelRequested
        if (r.errno !== 0 && !stopped) error = [error, failure(r, m.updateFailed)].filter(Boolean).join("\n")
        toast(r.errno === 0 ? m.updateSuccess : stopped ? m.updateStopped : m.updateFailed, r.errno === 0 || stopped ? "ok" : "err")
        await reload()
      } finally {
        updateRunning = false
        cancelling = false
        cancelRequested = false
        stopPending = undefined
        updateStop = undefined
        updateMonitor = undefined
        flushOutput()
      }
    })
  }

  function stopUpdate() {
    if (!updateStop || !updateRunning || cancelling || cancelRequested) return
    const stop = updateStop
    cancelling = true
    stopPending = (async () => {
      try {
        const r = await stop()
        if (disposed) return
        requireSuccess(r, m.stopFailed)
        cancelRequested = true
      } catch (e) {
        if (!disposed) error = [error, e instanceof Error ? e.message : String(e)].filter(Boolean).join("\n")
      } finally {
        cancelling = false
        flushOutput()
      }
    })()
    return stopPending
  }

  function shellQuote(value: string): string {
    return "'" + value.replace(/'/g, "'\"'\"'") + "'"
  }

  function verificationResult(verification: CertificateVerification, snapshot: KeyboxInfo): number {
    const statuses = snapshot.serials.map(serial => certificateStatusForSerial(verification, serial).status)
    if (statuses.some(status => status === "revoked" || status === "suspended")) return 1
    if (verification.completed && (verification.source === "live" || verification.source === "cache") &&
        verification.result === "not revoked" && statuses.length && statuses.every(status => status === "not-revoked")) return 0
    return 2
  }

  async function checkRevocation() {
    if (!bin) return
    await run("check", async () => {
      savedLogVisible = false
      setOutput("")
      clearCertificateCheck()
      try {
        const preSequence = infoSequence + 1
        const before = await loadInfo()
        if (preSequence !== infoSequence || !before || !before.exists || !before.structure_valid || !before.path.startsWith("/") ||
            /[\x00-\x1f\x7f]/.test(before.path) || !before.serials.length) throw new Error(m.checkFailed)
        const checkedIdentity = keyboxIdentity(before)
        const checkedSequence = infoSequence
        const r = await exec('"' + bin + '" verify ' + shellQuote(before.path), appendOutput, VERIFY_TIMEOUT)
        flushOutput()
        const uninterrupted = checkedSequence === infoSequence
        const postSequence = infoSequence + 1
        const after = await loadInfo()
        if (disposed) return
        if (!uninterrupted || postSequence !== infoSequence || !after || checkedIdentity !== keyboxIdentity(after)) {
          clearCertificateCheck()
          checkRc = 2
          error = m.checkFailed
          return
        }
        certificateVerification = parseCertificateStatus(r.stdout, r.failure ? null : r.errno)
        checkRc = verificationResult(certificateVerification, after)
        const validityLines = stripAnsi(r.stdout).trim().split("\n")
        const timeResult = /^RESULT: (expired|not_yet_valid) \(device time\)$/.exec(validityLines[validityLines.length - 1])
        if (!r.failure && r.errno === 3 && timeResult && certificateVerification.bySerial.size === 0 &&
            certificateVerification.source === "unknown" && validityLines.filter(line => line.startsWith("RESULT:")).length === 1 &&
            validityLines.some(line => /^\[FAIL\] validity: (expired|not_yet_valid) \([1-9][0-9]* certificates\)$/.exec(line)?.[1] === timeResult[1]) &&
            (!after.validity || after.validity.status === timeResult[1])) {
          checkRc = 3
          certificateVerification = null
        }
        if (r.failure || checkRc === 2) error = failure(r, m.checkFailed)
      } catch (e) {
        if (!disposed) {
          clearCertificateCheck()
          checkRc = 2
        }
        throw e
      } finally {
        flushOutput()
      }
    })
  }

  async function setSchedule(v: Schedule) {
    if (!moddir || busy !== null || schedule === v) return
    scheduleChoice = v
    try {
      await run("settings", async () => {
        const r = await exec('sh "' + moddir + '/settings.sh" set ' + v)
        requireSuccess(r, m.settingsFailed)
        await loadMeta()
        if (schedule !== v) throw new Error(m.settingsFailed)
      })
    } finally {
      scheduleChoice = null
    }
  }

  async function showLog() {
    if (!moddir) return
    await run("log", async () => {
      const r = await exec('if [ -f /data/local/tmp/keybox-update.log ]; then tail -n 80 /data/local/tmp/keybox-update.log; fi')
      requireSuccess(r, m.logFailed)
      setOutput(r.stdout.trim() || m.noLog)
      savedLogVisible = true
    })
  }

  onMount(() => {
    const media = window.matchMedia("(prefers-reduced-motion: reduce)")
    const syncMotion = () => { reducedMotion = media.matches }
    syncMotion()
    media.addEventListener("change", syncMotion)
    const colorMedia = window.matchMedia("(prefers-color-scheme: dark)")
    const syncColor = () => { systemDark = colorMedia.matches }
    syncColor()
    colorMedia.addEventListener("change", syncColor)
    enableEdgeToEdge()
    window.addEventListener("hashchange", syncRoute)
    syncRoute()
    void refresh()
    return () => {
      disposed = true
      updateMonitor?.abort()
      window.removeEventListener("hashchange", syncRoute)
      clearTimeout(outputTimer)
      outputTimer = undefined
      pendingOutput = ""
      media.removeEventListener("change", syncMotion)
      colorMedia.removeEventListener("change", syncColor)
    }
  })
</script>

<svelte:head><title>{targetsPage ? targetTitle : "Keybox Lite"}</title></svelte:head>

<div class="app-shell">
  <main class="mx-auto w-full max-w-[var(--page-width)] px-[var(--page-gutter)] pt-[var(--page-top)] pb-5">
    <header class={targetsPage ? "target-toolbar flex min-h-10 items-center gap-1.5" : "mb-3 flex items-center gap-2"}>
      {#if targetsPage}
        <a href="#/" class={ui.iconButton} aria-label={m.targetsBack} title={m.targetsBack} onclick={returnHome}><ArrowLeft size={18} /></a>
        <div class="target-toolbar-title min-w-0 flex-1 truncate text-sm font-semibold" class:is-visible={targetTitleVisible} aria-hidden={!targetTitleVisible}>{targetTitle}</div>
        <button id="targets-menu-trigger" type="button" class={ui.iconButton + " target-more"} aria-label={m.targetsMore} aria-haspopup="menu" aria-controls="targets-menu" aria-expanded={targetMenuOpen}
          onclick={() => targetManager?.toggleMenu()}><EllipsisVertical size={19} /></button>
      {:else}
        <img class="block size-11 shrink-0 max-[359px]:size-[38px]" src="./icon.svg" alt="" width="44" height="44" />
        <div class="min-w-0 flex-1 [&_h1]:text-xl [&_h1]:font-[650] [&_h1]:leading-[1.35] [&_h1]:tracking-[-.6px] [&_h1]:max-[359px]:text-lg [&_p]:mt-0.5 [&_p]:text-xs [&_p]:text-[var(--muted)]"><h1>Keybox Lite</h1><p>{m.subtitle}</p></div>
      <div class="flex items-center {ui.buttonGroup}">
        <Dropdown id="theme-options" label={m.theme} options={themeOptions} selected={theme} open={themeOpen} {reducedMotion} onOpenChange={toggleTheme} onSelect={setTheme}>
          {#snippet trigger()}
            {#if theme === "system"}<Monitor size={18} />{:else if theme === "light"}<Sun size={18} />{:else}<Moon size={18} />{/if}
          {/snippet}
          {#snippet optionIcon(option)}
            {#if option === "system"}<Monitor size={15} />{:else if option === "light"}<Sun size={15} />{:else}<Moon size={15} />{/if}
          {/snippet}
        </Dropdown>
        <Dropdown id="language-options" label={m.language} options={languageOptions} selected={lang} open={languageOpen} {reducedMotion} onOpenChange={toggleLanguage} onSelect={setLanguage}>
          {#snippet trigger()}<Languages size={18} />{/snippet}
        </Dropdown>
      </div>
      {/if}
    </header>

    <Toast message={error} active={!targetsPage} closeLabel={m.targetsClose} onclose={() => error = ""} />

    {#if targetsPage}
      <TargetManager {bin} {lang} disabled={!moddir} bind:this={targetManager} onMenuChange={(open) => targetMenuOpen = open} onTitleVisibility={(visible) => targetTitleVisible = visible} onTitleChange={(title) => targetTitle = title} />
    {:else}
    <div class="page-enter grid min-w-0 gap-3" out:pageExit>
    <section class="{ui.panel} relative" aria-busy={!initialDone || busy === "refresh"}>
      <div class="{ui.heading}">
        <h2 class={ui.headingTitle}><HardDrive size={17} />{m.installed}</h2>
        <span class="flex min-h-[18px] items-center gap-1.5 whitespace-nowrap text-[11px] text-[var(--muted)]" role="status">
          {#if !initialDone || busy === "refresh"}<Loader2 size={13} class="motion-safe:animate-spin" />{m.loading}{/if}
        </span>
      </div>
      <div class="min-h-[184px]">
        {#if !initialDone}
          <div  aria-hidden="true">
            <div class="mb-2 flex flex-wrap gap-1.5"><span class="block h-[21px] w-[74px] animate-pulse rounded-md bg-[var(--inset)] motion-reduce:animate-none"></span><span class="block h-[21px] w-[74px] animate-pulse rounded-md bg-[var(--inset)] motion-reduce:animate-none"></span></div>
            <div class="mb-2 block h-[13px] w-8 animate-pulse rounded-md bg-[var(--inset)] motion-reduce:animate-none"></div><div class="block h-[18px] w-[92%] animate-pulse rounded-md bg-[var(--inset)] motion-reduce:animate-none"></div>
            <div class="my-2.5 grid grid-cols-[.8fr_.7fr_1.5fr] gap-2 [&>div]:min-w-0 [&_dt]:text-[11px] [&_dt]:leading-snug [&_dt]:text-[var(--muted)] [&_dt]:[overflow-wrap:anywhere] [&_dd]:mt-0.5 [&_dd]:text-sm [&_dd]:font-[550] [&_dd]:tabular-nums">
              {#each [1, 2, 3] as item}<div class="block h-9 animate-pulse rounded-md bg-[var(--inset)] motion-reduce:animate-none"></div>{/each}
            </div>
            <div class="grid grid-cols-2 gap-1.5"><div class="block h-[52px] animate-pulse rounded-[9px] bg-[var(--inset)] motion-reduce:animate-none"></div><div class="block h-[52px] animate-pulse rounded-[9px] bg-[var(--inset)] motion-reduce:animate-none"></div></div>
          </div>
        {:else if info}
          <div  in:fade={{ duration: reducedMotion ? 0 : 260 }}>
            {#if infoStale}<p class="mb-1.5 text-[11px] text-[var(--red)]" role="status">{m.staleData}</p>{/if}
            {#if info.backend_error}<p class="mb-1.5 text-[11px] text-[var(--red)]" role="status">{info.backend_error}</p>{/if}
            <div class="mb-2 flex flex-wrap gap-1.5">
              {#if info.backend}<span class={badge + (!info.backend.active ? " bg-[var(--red-bg)] text-[var(--red)]" : " bg-[var(--inset)] text-[var(--muted)]")}>{info.backend.identity}{info.backend.active ? "" : " · " + m.backendInactive}</span>{/if}
              {#if info.exists}
                <span class="self-center text-[11px] leading-snug text-[var(--muted)]">XML</span>
                <span class={badge + (structureOk ? " bg-[var(--accent-bg)] text-[var(--accent)]" : " bg-[var(--red-bg)] text-[var(--red)]")}>
                  {#if structureOk}<ShieldCheck size={12} />{:else}<ShieldAlert size={12} />{/if}
                  {structureOk ? m.valid : m.invalid}
                </span>
              {:else}<span class={badge + " bg-[var(--inset)] text-[var(--muted)]"}>{m.noKeybox}</span>{/if}
            </div>
            <div ><span class="text-[11px] leading-snug text-[var(--muted)]">{m.path}</span><p class="mt-0.5 font-mono text-xs leading-normal [overflow-wrap:anywhere]">{info.path}</p></div>
            <dl class="my-2.5 grid grid-cols-[.8fr_.7fr_1.5fr] gap-2 [&>div]:min-w-0 [&_dt]:text-[11px] [&_dt]:leading-snug [&_dt]:text-[var(--muted)] [&_dt]:[overflow-wrap:anywhere] [&_dd]:mt-0.5 [&_dd]:text-sm [&_dd]:font-[550] [&_dd]:tabular-nums">
              <div><dt>{m.size}</dt><dd>{fmtSize(info.size)}</dd></div>
              <div><dt>{m.certs}</dt><dd>{info.serials.length}</dd></div>
              <div><dt>{m.modified}</dt><dd class="!text-[11px] !font-normal leading-relaxed">{fmtTime(info.modified)}</dd></div>
            </dl>
            {#if info.exists}
              <div class="grid grid-cols-2 gap-1.5">
                <div class="flex min-w-0 flex-col gap-1 rounded-[9px] bg-[var(--inset)] p-2">
                  <span class="text-[11px] leading-snug text-[var(--muted)]">{m.validity}</span>
                  <span class={statusValue + (info.validity?.status === "valid" ? " text-[var(--success)]" : info.validity?.status === "expired" || info.validity?.status === "not_yet_valid" ? " text-[var(--warning)]" : "")}>{validityLabel(info.validity?.status)}</span>
                </div>
                <div class="flex min-w-0 flex-col gap-1 rounded-[9px] bg-[var(--inset)] p-2" aria-live="polite">
                  <span class="text-[11px] leading-snug text-[var(--muted)]">{m.revocationStatus}</span>
                  <span class={statusValue + (checkRc === 1 ? " text-[var(--red)]" : checkRc === 0 ? " text-[var(--success)]" : "")}>
                    {#if busy === "check"}<Loader2 size={13} class="motion-safe:animate-spin" />{m.checking}
                    {:else}{checkRc === 0 ? m.notRevoked : checkRc === 1 ? m.revoked : checkRc === null ? m.unchecked : checkRc === 3 ? m.timeInvalid : m.unavailable}{/if}
                  </span>
                </div>
              </div>
            {/if}
          </div>
        {:else}
          <div class="flex min-h-[170px] flex-col items-center justify-center gap-3 text-[13px] text-[var(--muted)]" in:fade={{ duration: reducedMotion ? 0 : 180 }}><ShieldQuestion size={26} /><p>{m.readFailed}</p></div>
        {/if}
      </div>
    </section>

    <a href="#/targets" class="{ui.panel} {ui.panelRow} text-[var(--text)] transition-colors hover:bg-[var(--inset)]"><span class={ui.headingTitle}><ListChecks size={17} />{m.targetsTitle}</span><ChevronRight size={17} /></a>

    <CertificateDetails certificates={info?.validity?.certificates} serials={info?.serials}
      verification={certificateVerification} integrity={info?.integrity} messages={m} bind:open={detailsOpen} />

    <section class="{ui.panel}">
      <div class="grid min-h-8 grid-cols-[minmax(0,1fr)_auto] items-center gap-2">
        <h2 class={ui.headingTitle}>{m.keyboxSource}</h2>
        <Dropdown id="keybox-source-options" label={m.keyboxSource} options={sourceOptions} selected={keyboxSource}
          open={sourceOpen} {reducedMotion} disabled={busy !== null || !moddir} describedBy="keybox-source-hint"
          triggerClass={ui.selectButton} onOpenChange={toggleSource} onSelect={setSource}>
          {#snippet trigger()}
            <span class="grid min-w-0 text-start">
              {#each sourceOptions as option}
                <span class="col-start-1 row-start-1 truncate" class:invisible={option.value !== keyboxSource} aria-hidden={option.value !== keyboxSource}>{option.label}</span>
              {/each}
            </span>
            <ChevronDown size={14} class={"text-[var(--muted)] transition-transform " + (sourceOpen ? "rotate-180" : "")} />
          {/snippet}
        </Dropdown>
      </div>
      <p id="keybox-source-hint" class="mt-2 text-[11px] leading-relaxed text-[var(--muted)]">{m.sourceManualHint}</p>
      <div class="mt-3 border-t border-[var(--line)] pt-3">
        <div class="flex min-h-8 items-center justify-between gap-2">
          <span id="skip-revocation-label" class="text-xs font-[550]">{m.skipRevocation}</span>
          <button type="button" role="switch" aria-checked={skipRevocation} aria-labelledby="skip-revocation-label" aria-describedby="skip-revocation-hint"
            disabled={busy !== null || !moddir} onclick={() => setSkipRevocation(!skipRevocation)}
            class="grid size-8 shrink-0 place-items-center rounded-lg disabled:opacity-50">
            <span class={"flex h-[18px] w-8 items-center rounded-full p-0.5 transition-colors duration-150 " + (skipRevocation ? "bg-[var(--accent)]" : "bg-[var(--line)]")}>
              <span class={"size-[14px] rounded-full bg-[var(--panel)] shadow-sm transition-transform duration-150 " + (skipRevocation ? "translate-x-[14px]" : "")}></span>
            </span>
          </button>
        </div>
        <p id="skip-revocation-hint" class="mt-1 text-[11px] leading-relaxed text-[var(--muted)]">{m.skipRevocationHint}</p>
        <p class="mt-1 text-[11px] leading-relaxed text-[var(--muted)]">{m.revocationCacheHint}</p>
      </div>
    </section>

    <div class="grid grid-cols-2 {ui.buttonGroup}">
      <button class={ui.button + " col-span-full " + (busy === "update" ? "bg-[var(--red-bg)] text-[var(--red)]" : "bg-[var(--accent)] text-[var(--accent-ink)] hover:enabled:brightness-95")} onclick={busy === "update" ? stopUpdate : update}
        disabled={!moddir || (busy === "update" ? !updateRunning || cancelling || cancelRequested : busy !== null)}>
        {#if busy === "update"}
          {#if cancelling || cancelRequested || !updateRunning}<Loader2 size={17} class="motion-safe:animate-spin" />{:else}<Square size={17} />{/if}
          {cancelling ? m.stopping : cancelRequested ? m.stopRequested : !updateRunning ? m.refreshing : m.stopUpdate}
        {:else}<Download size={17} />{m.update}{/if}
      </button>
      <button class="{ui.button} border border-[var(--line)] bg-[var(--panel)] hover:enabled:bg-[var(--inset)]" onclick={checkRevocation} disabled={busy !== null || !moddir}>
        {#if busy === "check"}<Loader2 size={16} class="motion-safe:animate-spin" />{:else}<ShieldCheck size={16} />{/if}
        {busy === "check" ? m.checking : m.check}
      </button>
      <button class="{ui.button} border border-[var(--line)] bg-[var(--panel)] hover:enabled:bg-[var(--inset)]" onclick={refresh} disabled={busy !== null}>
        {#if busy === "refresh"}<Loader2 size={16} class="motion-safe:animate-spin" />{:else}<RefreshCw size={16} />{/if}
        {busy === "refresh" ? m.refreshing : m.refresh}
      </button>
    </div>

    <section class="{ui.panel}">
      <div class="{ui.heading}"><h2 class={ui.headingTitle}><CalendarClock size={17} />{m.schedule}</h2></div>
      <div class="schedule-control" role="group" aria-label={m.schedule} aria-busy={scheduleChoice !== null}>
        <span class="schedule-indicator" class:is-ready={displayedSchedule !== null}
          style:transform={"translateX(calc(" + Math.max(0, schedules.indexOf(displayedSchedule!)) * 100 + "% + " + Math.max(0, schedules.indexOf(displayedSchedule!)) * 8 + "px))"} aria-hidden="true"></span>
        {#each schedules as opt}
          <button onclick={() => setSchedule(opt)} disabled={busy !== null || !moddir} aria-pressed={displayedSchedule === opt}
            class:is-active={displayedSchedule === opt}>{m[opt]}</button>
        {/each}
      </div>
      <div class="min-h-[18px] mt-2">
        {#if schedule && schedule !== "off"}<p class="text-[11px] leading-relaxed text-[var(--muted)]" transition:slide={{ duration: reducedMotion ? 0 : 160 }}>{m.lastRun}: {lastRun ? fmtTime(lastRun) : "—"}</p>{/if}
      </div>
    </section>

    <section class="{ui.panel}">
      <div class="{ui.heading}"><h2 class={ui.headingTitle}><History size={17} />{m.records}</h2>{#if version}<span class="whitespace-nowrap rounded-md bg-[var(--inset)] px-1.5 py-0.5 text-[11px] text-[var(--muted)]">v{version}</span>{/if}</div>
      {#if !initialDone}
        <div class="grid gap-1.5" aria-hidden="true"><div class="block h-[18px] w-[92%] animate-pulse rounded-md bg-[var(--inset)] motion-reduce:animate-none"></div><div class="block h-[18px] w-[92%] animate-pulse rounded-md bg-[var(--inset)] motion-reduce:animate-none"></div></div>
      {:else if records.length}
        <div class="max-h-[180px] overflow-y-auto [&_p]:border-b [&_p]:border-[var(--line)] [&_p]:py-1.5 [&_p]:font-mono [&_p]:text-[11px] [&_p]:leading-normal [&_p]:text-[var(--muted)] [&_p]:[overflow-wrap:anywhere] [&_p:last-child]:border-0" in:fade={{ duration: reducedMotion ? 0 : 220 }}>{#each records as r}<p>{r}</p>{/each}</div>
      {:else}<p class="text-[11px] leading-relaxed text-[var(--muted)]">{m.noRecords}</p>{/if}
    </section>

    <LogOutput {output} lines={outputLines} title={savedLogVisible ? m.savedUpdateLog : m.output} hint={m.hint}
      viewSavedLogLabel={m.savedUpdateLog} disabled={busy !== null || !moddir} onViewSavedLog={showLog} />
    </div>
    {/if}
  </main>
</div>
