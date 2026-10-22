<script lang="ts">
  import { onMount } from "svelte"
  import { fade } from "svelte/transition"
  import {
    RefreshCw, Loader2, ShieldCheck, ShieldAlert, ShieldQuestion,
    Terminal, Languages, HardDrive, Fingerprint, Download,
    CalendarClock, History, ChevronDown, Monitor, Sun, Moon, Check, Square
  } from "lucide-svelte"
  import { exec, toast, enableEdgeToEdge, type ExecResult } from "./lib/bridge"
  import { messages, detect, type Locale } from "./lib/i18n"

  const MODULE_ID = "keybox-lite"

  let lang = $state<Locale>(detect())
  const m = $derived(messages[lang])

  type Theme = "system" | "light" | "dark"
  const themes: Theme[] = ["system", "light", "dark"]
  let themeOpen = $state(false)
  let themeControl: HTMLDivElement
  let themeButton: HTMLButtonElement
  function dismissTheme(event: Event) {
    if (event.target instanceof Node && !themeControl?.contains(event.target)) themeOpen = false
  }
  function themeKeydown(event: KeyboardEvent) {
    if (event.key === "Escape" && themeOpen) {
      themeOpen = false
      themeButton?.focus()
    }
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
    themeButton?.focus()
    try { localStorage.setItem("keybox-lite-theme", value) } catch { /* Keep the in-session choice. */ }
  }

  // Downloader attempts have bounded waits; updates also have a 20-minute supervisor deadline.
  // Update can try multiple catalog candidates and three sources; this is a UI
  // waiting budget, not a total backend deadline. Neither timeout cancels the job.
  const VERIFY_TIMEOUT = 30 * 60_000
  const UPDATE_TIMEOUT = 75 * 60_000
  type Busy = "update" | "check" | "refresh" | "settings" | "log"
  type Schedule = "off" | "daily" | "weekly"
  type ValidityStatus = "valid" | "expired" | "not_yet_valid" | "unknown"
  interface CertificateValidity {
    serial: string
    not_before: number
    not_after: number
    status: ValidityStatus
  }
  interface KeyboxInfo {
    path: string
    exists: boolean
    size: number
    modified: number
    structure_valid: boolean
    serials: string[]
    validity?: { status: ValidityStatus; certificates: CertificateValidity[] }
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
  let error = $state("")
  let checkRc = $state<number | null>(null)
  let busy = $state<Busy | null>(null)
  let cancelling = $state(false)
  let cancelRequested = $state(false)

  const schedules: Schedule[] = ["off", "daily", "weekly"]
  let schedule = $state<Schedule | null>(null)
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

  async function loadInfo() {
    // Keep the previous snapshot while loading; mark it stale if the read fails.
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
    if (v.backend != null) {
      const backend = v.backend as Record<string, unknown>
      if (typeof backend !== "object" || typeof backend.identity !== "string" || typeof backend.active !== "boolean") throw new Error(m.invalidInfo)
    }
    if (v.backend_error !== undefined && typeof v.backend_error !== "string") throw new Error(m.invalidInfo)
    info = v as unknown as KeyboxInfo
    infoStale = false
  }

  async function loadMeta() {
    const s = await exec('sh "' + moddir + '/settings.sh" get')
    requireSuccess(s, m.settingsFailed)
    const next = kv(s.stdout, "SCHEDULE")
    const last = Number(kv(s.stdout, "LAST_RUN"))
    if (!schedules.includes(next as Schedule) || !Number.isFinite(last) || last < 0) {
      throw new Error(m.settingsFailed)
    }
    schedule = next as Schedule
    lastRun = last
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
        if (load === loadInfo) infoStale = true
        errors.push(e instanceof Error ? e.message : String(e))
      }
    }
    if (errors.length) throw new Error(errors.join("\n"))
  }

  async function refresh() {
    await run("refresh", async () => {
      checkRc = null
      if (!moddir) await resolveDir()
      await reload()
    })
  }

  function appendOutput(line: string) {
    output += line + "\n"
  }

  async function update() {
    if (!moddir) return
    await run("update", async () => {
      output = ""
      checkRc = null
      cancelRequested = false
      const r = await exec('sh "' + moddir + '/action.sh"', appendOutput, UPDATE_TIMEOUT)
      if (r.errno !== 0 && !cancelRequested) error = failure(r, m.updateFailed)
      toast(r.errno === 0 ? m.updateSuccess : cancelRequested ? m.updateStopped : m.updateFailed, r.errno === 0 || cancelRequested ? "ok" : "err")
      await reload()
    })
  }

  async function stopUpdate() {
    if (!bin || cancelling) return
    cancelling = true
    cancelRequested = true
    try {
      const r = await exec('"' + bin + '" cancel-update /data/adb/keybox-lite', appendOutput)
      requireSuccess(r, m.stopFailed)
    } catch (e) {
      cancelRequested = false
      error = e instanceof Error ? e.message : String(e)
    } finally {
      cancelling = false
    }
  }

  async function checkRevocation() {
    if (!bin) return
    await run("check", async () => {
      output = ""
      checkRc = null
      const r = await exec('"' + bin + '" verify', appendOutput, VERIFY_TIMEOUT)
      checkRc = r.errno
      // 1 = revoked, 2 = unavailable, 3 = outside certificate validity period.
      if (r.failure || ![0, 1, 2, 3].includes(r.errno)) error = failure(r, m.checkFailed)
    })
  }

  async function setSchedule(v: Schedule) {
    if (!moddir) return
    await run("settings", async () => {
      const r = await exec('sh "' + moddir + '/settings.sh" set ' + v)
      requireSuccess(r, m.settingsFailed)
      await loadMeta()
      if (schedule !== v) throw new Error(m.settingsFailed)
    })
  }

  async function showLog() {
    if (!moddir) return
    await run("log", async () => {
      const r = await exec('if [ -f /data/local/tmp/keybox-update.log ]; then tail -n 80 /data/local/tmp/keybox-update.log; fi')
      requireSuccess(r, m.logFailed)
      output = r.stdout.trim() || m.noLog
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
    void refresh()
    return () => {
      media.removeEventListener("change", syncMotion)
      colorMedia.removeEventListener("change", syncColor)
    }
  })
</script>

<svelte:window onpointerdown={dismissTheme} onfocusin={dismissTheme} onkeydown={themeKeydown} />

<div class="app-shell">
  <main class="page">
    <header class="page-header">
      <img class="brand-icon" src="./icon.svg" alt="" width="44" height="44" />
      <div class="brand"><h1>Keybox Lite</h1><p>{m.subtitle}</p></div>
      <div class="header-actions">
        <div class="theme-control" bind:this={themeControl}>
          <button class="icon-button" bind:this={themeButton} onclick={() => (themeOpen = !themeOpen)} title={m.theme} aria-label={m.theme} aria-expanded={themeOpen} aria-controls="theme-options">
            {#if theme === "system"}<Monitor size={18} />{:else if theme === "light"}<Sun size={18} />{:else}<Moon size={18} />{/if}
          </button>
          {#if themeOpen}
            <div id="theme-options" class="theme-options" role="group" aria-label={m.theme} in:fade={{ duration: reducedMotion ? 0 : 140 }}>
              {#each themes as option}
                <button onclick={() => setTheme(option)} aria-pressed={theme === option} class:active={theme === option}>
                  {#if option === "system"}<Monitor size={15} />{:else if option === "light"}<Sun size={15} />{:else}<Moon size={15} />{/if}
                  <span>{option === "system" ? m.themeSystem : option === "light" ? m.themeLight : m.themeDark}</span>
                  {#if theme === option}<Check size={14} />{/if}
                </button>
              {/each}
            </div>
          {/if}
        </div>
        <button class="icon-button" onclick={() => (lang = lang === "zh" ? "en" : "zh")} title={m.language} aria-label={m.language}><Languages size={18} /></button>
      </div>
    </header>

    {#if error}
      <div role="alert" class="error-message" in:fade={{ duration: reducedMotion ? 0 : 180 }}>
        <ShieldAlert size={17} /><span>{error}</span>
      </div>
    {/if}

    <section class="panel overview" aria-busy={!initialDone || busy === "refresh"}>
      <div class="panel-heading">
        <h2><HardDrive size={17} />{m.installed}</h2>
        <span class="subtle loading-label" role="status">
          {#if !initialDone || busy === "refresh"}<Loader2 size={13} class="spin" />{m.loading}{/if}
        </span>
      </div>
      <div class="overview-body">
        {#if !initialDone}
          <div class="skeleton-content" aria-hidden="true">
            <div class="badge-row"><span class="skeleton sk-badge"></span><span class="skeleton sk-badge"></span></div>
            <div class="skeleton sk-label"></div><div class="skeleton sk-path"></div>
            <div class="facts-grid">
              {#each [1, 2, 3] as item}<div class="skeleton sk-fact"></div>{/each}
            </div>
            <div class="status-grid"><div class="skeleton sk-status"></div><div class="skeleton sk-status"></div></div>
          </div>
        {:else if info}
          <div class="loaded-content" in:fade={{ duration: reducedMotion ? 0 : 260 }}>
            {#if infoStale}<p class="stale-note" role="status">{m.staleData}</p>{/if}
            {#if info.backend_error}<p class="stale-note" role="status">{info.backend_error}</p>{/if}
            <div class="badge-row">
              {#if info.backend}<span class="badge" class:bad={!info.backend.active}>{info.backend.identity}{info.backend.active ? "" : " · " + m.backendInactive}</span>{/if}
              {#if info.exists}
                <span class="field-label badge-caption">XML</span>
                <span class="badge" class:good={structureOk} class:bad={!structureOk}>
                  {#if structureOk}<ShieldCheck size={12} />{:else}<ShieldAlert size={12} />{/if}
                  {structureOk ? m.valid : m.invalid}
                </span>
              {:else}<span class="badge">{m.noKeybox}</span>{/if}
            </div>
            <div class="path-block"><span class="field-label">{m.path}</span><p class="file-path">{info.path}</p></div>
            <dl class="facts-grid">
              <div><dt>{m.size}</dt><dd>{fmtSize(info.size)}</dd></div>
              <div><dt>{m.certs}</dt><dd>{info.serials.length}</dd></div>
              <div><dt>{m.modified}</dt><dd class="date-value">{fmtTime(info.modified)}</dd></div>
            </dl>
            {#if info.exists}
              <div class="status-grid">
                <div class="status-tile">
                  <span class="field-label">{m.validity}</span>
                  <span class="status-value" class:bad={info.validity?.status === "expired" || info.validity?.status === "not_yet_valid"} class:good={info.validity?.status === "valid"}>{validityLabel(info.validity?.status)}</span>
                </div>
                <div class="status-tile" aria-live="polite">
                  <span class="field-label">{m.revocationStatus}</span>
                  <span class="status-value" class:bad={checkRc === 1} class:not-revoked={checkRc === 0}>
                    {#if busy === "check"}<Loader2 size={13} class="spin" />{m.checking}
                    {:else}{checkRc === 0 ? m.notRevoked : checkRc === 1 ? m.revoked : checkRc === null ? m.unchecked : checkRc === 3 ? m.timeInvalid : m.unavailable}{/if}
                  </span>
                </div>
              </div>
            {/if}
          </div>
        {:else}
          <div class="empty-state" in:fade={{ duration: reducedMotion ? 0 : 180 }}><ShieldQuestion size={26} /><p>{m.readFailed}</p></div>
        {/if}
      </div>
    </section>

    <div class="action-grid">
      <button class="button primary" onclick={update} disabled={busy !== null || !moddir}>
        {#if busy === "update"}<Loader2 size={17} class="spin" />{:else}<Download size={17} />{/if}
        {busy === "update" ? m.updating : m.update}
      </button>
      <button class="button secondary" onclick={checkRevocation} disabled={busy !== null || !moddir}>
        {#if busy === "check"}<Loader2 size={16} class="spin" />{:else}<ShieldCheck size={16} />{/if}
        {busy === "check" ? m.checking : m.check}
      </button>
      <button class="button secondary" onclick={refresh} disabled={busy !== null}>
        {#if busy === "refresh"}<Loader2 size={16} class="spin" />{:else}<RefreshCw size={16} />{/if}
        {busy === "refresh" ? m.refreshing : m.refresh}
      </button>
    </div>

    <button class="stop-update" onclick={stopUpdate} disabled={cancelling || !moddir}>
      {#if cancelling}<Loader2 size={14} class="spin" />{:else}<Square size={13} />{/if}
      {cancelling ? m.stopping : m.stopUpdate}
    </button>

    <section class="panel certificate-panel">
      <button class="disclosure" onclick={() => (detailsOpen = !detailsOpen)} aria-expanded={detailsOpen} aria-controls="certificate-details" disabled={!info?.serials.length}>
        <span><Fingerprint size={17} />{m.certificateDetails}</span>
        <span class="disclosure-end"><span class="count">{info?.serials.length ?? "—"}</span><ChevronDown size={16} class={detailsOpen ? "chevron open" : "chevron"} /></span>
      </button>
      <div id="certificate-details" class="details-reveal" class:expanded={detailsOpen} inert={!detailsOpen}>
        <div class="details-reveal-inner">
          <div class="certificate-list">
            <p class="detail-note">{m.deviceTimeNote}</p>
            {#if info?.validity?.certificates.length}
              {#each info.validity.certificates as cert, index}
                <article class="certificate">
                  <div class="certificate-heading"><h3>{m.certificate} {index + 1}</h3><span class="badge" class:bad={cert.status === "expired" || cert.status === "not_yet_valid"}>{validityLabel(cert.status)}</span></div>
                  <p class="serial">{cert.serial}</p>
                  <dl class="certificate-dates">
                    <div><dt>{m.notBefore}</dt><dd>{new Date(cert.not_before * 1000).toLocaleString()}</dd></div>
                    <div><dt>{m.notAfter}</dt><dd>{new Date(cert.not_after * 1000).toLocaleString()}</dd></div>
                  </dl>
                </article>
              {/each}
            {:else}
              {#each info?.serials ?? [] as sn}<p class="serial">{sn}</p>{/each}
            {/if}
          </div>
        </div>
      </div>
    </section>

    <section class="panel">
      <div class="panel-heading"><h2><CalendarClock size={17} />{m.schedule}</h2></div>
      <div class="segmented-control">
        {#each schedules as opt}
          <button onclick={() => setSchedule(opt)} disabled={busy !== null || !moddir} aria-pressed={schedule === opt} class:active={schedule === opt}>{m[opt]}</button>
        {/each}
      </div>
      {#if schedule && schedule !== "off"}<p class="detail-note schedule-note">{m.lastRun}: {lastRun ? fmtTime(lastRun) : "—"}</p>{/if}
    </section>

    <section class="panel">
      <div class="panel-heading"><h2><History size={17} />{m.records}</h2>{#if version}<span class="count">v{version}</span>{/if}</div>
      {#if !initialDone}
        <div class="records-skeleton" aria-hidden="true"><div class="skeleton sk-path"></div><div class="skeleton sk-path"></div></div>
      {:else if records.length}
        <div class="record-list" in:fade={{ duration: reducedMotion ? 0 : 220 }}>{#each records as r}<p>{r}</p>{/each}</div>
      {:else}<p class="detail-note">{m.noRecords}</p>{/if}
    </section>

    <section class="panel output-panel">
      <div class="panel-heading"><h2><Terminal size={17} />{m.output}</h2><button class="text-button" onclick={showLog} disabled={busy !== null || !moddir}>{m.showLog}</button></div>
      <pre>{output || m.hint}</pre>
    </section>
  </main>
</div>
