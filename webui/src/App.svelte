<script lang="ts">
  import { onMount } from "svelte"
  import { fly } from "svelte/transition"
  import {
    KeyRound, RefreshCw, Loader2, ShieldCheck, ShieldAlert, ShieldQuestion,
    Terminal, Languages, Clock, HardDrive, Fingerprint, Download, Sun, Moon,
    CalendarClock, History
  } from "lucide-svelte"
  import { exec, toast, enableEdgeToEdge } from "./lib/bridge"
  import { messages, detect, type Locale } from "./lib/i18n"

  const MODULE_ID = "keybox-lite"

  let lang = $state<Locale>(detect())
  const m = $derived(messages[lang])

  let dark = $state(typeof matchMedia !== "undefined" ? matchMedia("(prefers-color-scheme: dark)").matches : true)
  $effect(() => {
    document.documentElement.classList.toggle("dark", dark)
  })

  let moddir = $state("")
  let info = $state<any>(null)
  let output = $state("")
  let error = $state("")
  let checkRc = $state<number | null>(null)
  let busy = $state<"update" | "check" | "refresh" | "settings" | null>(null)

  let schedule = $state("off")
  let lastRun = $state(0)
  let version = $state("")
  let records = $state<string[]>([])

  const bin = $derived(moddir ? moddir + "/bin/keyboxlite" : "")
  const structureOk = $derived(info?.structure_valid === true)

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

  async function resolveDir() {
    const cmd =
      "[ -d /data/adb/modules/" + MODULE_ID + " ] && echo /data/adb/modules/" + MODULE_ID +
      " || echo /data/adb/modules_update/" + MODULE_ID
    const r = await exec(cmd)
    moddir = r.stdout.trim()
  }

  async function refresh() {
    if (!bin) return
    busy = "refresh"
    error = ""
    const r = await exec(bin + " info")
    if (r.errno === 0) {
      try {
        info = JSON.parse(r.stdout)
      } catch (e) {
        error = String(e)
      }
    } else {
      error = r.stderr || "info failed"
    }
    busy = null
    await loadMeta()
  }

  async function loadMeta() {
    if (!moddir) return
    const s = await exec("sh " + moddir + "/settings.sh get")
    schedule = kv(s.stdout, "SCHEDULE") || "off"
    lastRun = Number(kv(s.stdout, "LAST_RUN")) || 0
    const rec = await exec(
      "grep '^version=' " + moddir + "/module.prop 2>/dev/null; echo '---'; tail -n 20 /data/local/tmp/keybox-update.history 2>/dev/null"
    )
    const parts = rec.stdout.split("---")
    version = kv(parts[0] || "", "version")
    records = (parts[1] || "")
      .split("\n")
      .map((x) => x.trim())
      .filter(Boolean)
      .reverse()
  }

  async function update() {
    busy = "update"
    output = ""
    error = ""
    checkRc = null
    const r = await exec("sh " + moddir + "/action.sh", (line) => {
      output = (output + line + "\n").trimStart()
    })
    output = (r.stdout + r.stderr).trim() || output
    toast(r.errno === 0 ? "Keybox updated" : "Update failed", r.errno === 0 ? "ok" : "err")
    busy = null
    await refresh()
  }

  async function checkRevocation() {
    busy = "check"
    output = ""
    error = ""
    checkRc = null
    const r = await exec(bin + " verify", (line) => {
      output = (output + line + "\n").trimStart()
    })
    output = (r.stdout + r.stderr).trim() || output
    checkRc = r.errno
    busy = null
  }

  async function setSchedule(v: string) {
    busy = "settings"
    await exec("sh " + moddir + "/settings.sh set " + v)
    await loadMeta()
    busy = null
  }

  async function showLog() {
    const r = await exec("tail -n 80 /data/local/tmp/keybox-update.log 2>/dev/null")
    output = (r.stdout || r.stderr).trim()
    checkRc = null
  }

  onMount(async () => {
    enableEdgeToEdge()
    await resolveDir()
    await refresh()
  })
</script>

<div
  class="min-h-dvh bg-neutral-100 text-neutral-900 antialiased dark:bg-neutral-950 dark:text-neutral-100"
  style="padding-top: env(safe-area-inset-top); padding-bottom: env(safe-area-inset-bottom); padding-left: env(safe-area-inset-left); padding-right: env(safe-area-inset-right);"
>
  <div class="mx-auto max-w-2xl px-4 pb-16 pt-8">
    <header class="mb-6 flex items-center gap-3">
      <div class="flex size-11 items-center justify-center rounded-2xl bg-emerald-500/15 text-emerald-600 dark:text-emerald-400">
        <KeyRound size={22} />
      </div>
      <div class="flex-1">
        <h1 class="text-lg font-semibold">Keybox Lite</h1>
        <p class="text-xs text-neutral-500 dark:text-neutral-400">{m.subtitle}</p>
      </div>
      <button
        onclick={() => (dark = !dark)}
        class="flex size-9 shrink-0 items-center justify-center rounded-lg border border-neutral-300 text-neutral-600 hover:bg-neutral-200 dark:border-neutral-700 dark:text-neutral-300 dark:hover:bg-neutral-800"
        title="theme"
      >
        {#if dark}<Sun size={16} />{:else}<Moon size={16} />{/if}
      </button>
      <button
        onclick={() => (lang = lang === "zh" ? "en" : "zh")}
        class="flex size-9 shrink-0 items-center justify-center rounded-lg border border-neutral-300 text-neutral-600 hover:bg-neutral-200 dark:border-neutral-700 dark:text-neutral-300 dark:hover:bg-neutral-800"
        title="language"
      >
        <Languages size={16} />
      </button>
    </header>

    {#if error}
      <div class="mb-4 flex items-start gap-2 rounded-xl border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-600 dark:text-red-300">
        <ShieldAlert size={18} class="mt-0.5 shrink-0" />
        <span class="break-all">{error}</span>
      </div>
    {/if}

    <section class="mb-4 rounded-2xl border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-900/60">
      <div class="mb-3 flex items-center justify-between">
        <h2 class="flex items-center gap-2 text-sm font-medium text-neutral-600 dark:text-neutral-300">
          <HardDrive size={16} /> {m.installed}
        </h2>
        <div class="flex items-center gap-2">
          {#if checkRc !== null}
            <span
              transition:fly={{ y: -4, duration: 180 }}
              class="flex items-center gap-1 rounded-full px-2 py-0.5 text-xs {checkRc === 0 ? 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400' : checkRc === 1 ? 'bg-red-500/15 text-red-600 dark:text-red-400' : 'bg-amber-500/15 text-amber-600 dark:text-amber-400'}"
            >
              {#if checkRc === 0}<ShieldCheck size={12} />{:else if checkRc === 1}<ShieldAlert size={12} />{:else}<ShieldQuestion size={12} />{/if}
              {checkRc === 0 ? m.notRevoked : checkRc === 1 ? m.revoked : m.unavailable}
            </span>
          {/if}
          {#if info?.exists}
            <span class="flex items-center gap-1 rounded-full px-2 py-0.5 text-xs {structureOk ? 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400' : 'bg-red-500/15 text-red-600 dark:text-red-400'}">
              {#if structureOk}<ShieldCheck size={12} />{:else}<ShieldAlert size={12} />{/if}
              {structureOk ? m.valid : m.invalid}
            </span>
          {:else}
            <span class="rounded-full bg-neutral-200 px-2 py-0.5 text-xs text-neutral-500 dark:bg-neutral-700/40 dark:text-neutral-400">{m.none}</span>
          {/if}
        </div>
      </div>

      <dl class="grid grid-cols-1 gap-2 text-sm sm:grid-cols-2">
        <div class="flex items-center gap-2 text-neutral-500 dark:text-neutral-400">
          <span class="shrink-0">{m.path}</span>
          <span class="truncate font-mono text-xs text-neutral-800 dark:text-neutral-200">{info?.path ?? "--"}</span>
        </div>
        <div class="flex items-center gap-2 text-neutral-500 dark:text-neutral-400">
          <span>{m.size}</span><span class="text-neutral-800 dark:text-neutral-200">{fmtSize(info?.size ?? 0)}</span>
        </div>
        <div class="flex items-center gap-2 text-neutral-500 dark:text-neutral-400">
          <Clock size={14} /><span class="text-neutral-800 dark:text-neutral-200">{fmtTime(info?.modified ?? 0)}</span>
        </div>
        <div class="flex items-center gap-2 text-neutral-500 dark:text-neutral-400">
          <Fingerprint size={14} /><span class="text-neutral-800 dark:text-neutral-200">{(info?.serials?.length ?? 0)} {m.certs}</span>
        </div>
      </dl>

      {#if info?.serials?.length}
        <div class="mt-3 space-y-1">
          {#each info.serials as sn}
            <div class="truncate rounded-lg bg-neutral-100 px-2 py-1 font-mono text-[11px] text-neutral-500 dark:bg-neutral-950/60 dark:text-neutral-500">{sn}</div>
          {/each}
        </div>
      {/if}
    </section>

    <div class="mb-3 grid grid-cols-2 gap-3">
      <button
        onclick={update}
        disabled={busy !== null}
        class="col-span-2 flex items-center justify-center gap-2 rounded-xl bg-emerald-600 py-3 text-sm font-medium text-white transition hover:bg-emerald-500 disabled:opacity-50"
      >
        {#if busy === "update"}<Loader2 size={16} class="animate-spin" />{:else}<Download size={16} />{/if}
        {busy === "update" ? m.updating : m.update}
      </button>
      <button
        onclick={checkRevocation}
        disabled={busy !== null}
        class="flex items-center justify-center gap-2 rounded-xl border border-neutral-300 py-3 text-sm font-medium text-neutral-700 transition hover:bg-neutral-200 disabled:opacity-50 dark:border-neutral-700 dark:text-neutral-200 dark:hover:bg-neutral-800"
      >
        {#if busy === "check"}<Loader2 size={16} class="animate-spin" />{:else}<ShieldCheck size={16} />{/if}
        {busy === "check" ? m.checking : m.check}
      </button>
      <button
        onclick={refresh}
        disabled={busy !== null}
        class="flex items-center justify-center gap-2 rounded-xl border border-neutral-300 py-3 text-sm font-medium text-neutral-700 transition hover:bg-neutral-200 disabled:opacity-50 dark:border-neutral-700 dark:text-neutral-200 dark:hover:bg-neutral-800"
      >
        {#if busy === "refresh"}<Loader2 size={16} class="animate-spin" />{:else}<RefreshCw size={16} />{/if}
        {busy === "refresh" ? m.refreshing : m.refresh}
      </button>
    </div>

    <section class="mb-4 rounded-2xl border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-900/60">
      <h2 class="flex items-center gap-2 text-sm font-medium text-neutral-600 dark:text-neutral-300">
        <CalendarClock size={16} /> {m.schedule}
      </h2>
      <div class="mt-3 grid grid-cols-3 gap-2">
        {#each [["off", m.off], ["daily", m.daily], ["weekly", m.weekly]] as opt}
          <button
            onclick={() => setSchedule(opt[0])}
            disabled={busy !== null}
            class="flex items-center justify-center rounded-lg border py-2 text-sm font-medium transition disabled:opacity-50 {schedule === opt[0] ? 'border-emerald-500 bg-emerald-500/15 text-emerald-600 dark:text-emerald-400' : 'border-neutral-300 text-neutral-600 hover:bg-neutral-200 dark:border-neutral-700 dark:text-neutral-300 dark:hover:bg-neutral-800'}"
          >
            {opt[1]}
          </button>
        {/each}
      </div>
      {#if schedule !== "off"}
        <p class="mt-2 text-xs text-neutral-500 dark:text-neutral-400">{m.lastRun}: {lastRun ? fmtTime(lastRun) : "--"}</p>
      {/if}
    </section>

    <section class="mb-4 rounded-2xl border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-900/60">
      <div class="mb-3 flex items-center justify-between">
        <h2 class="flex items-center gap-2 text-sm font-medium text-neutral-600 dark:text-neutral-300">
          <History size={16} /> {m.records}
        </h2>
        {#if version}<span class="rounded-full bg-neutral-200 px-2 py-0.5 text-xs text-neutral-600 dark:bg-neutral-700/40 dark:text-neutral-300">{m.version} {version}</span>{/if}
      </div>
      {#if records.length}
        <div class="space-y-1">
          {#each records as r}
            <div class="truncate rounded-lg bg-neutral-100 px-2 py-1 font-mono text-[11px] text-neutral-500 dark:bg-neutral-950/60 dark:text-neutral-500">{r}</div>
          {/each}
        </div>
      {:else}
        <p class="text-xs text-neutral-500 dark:text-neutral-400">{m.noRecords}</p>
      {/if}
    </section>

    <section class="rounded-2xl border border-neutral-200 bg-white dark:border-neutral-800 dark:bg-neutral-900/60">
      <div class="flex items-center justify-between border-b border-neutral-200 px-4 py-2 dark:border-neutral-800">
        <h2 class="flex items-center gap-2 text-sm font-medium text-neutral-600 dark:text-neutral-300">
          <Terminal size={16} /> {m.output}
        </h2>
        <button onclick={showLog} class="text-xs text-neutral-500 hover:text-neutral-800 dark:text-neutral-400 dark:hover:text-neutral-200">{m.showLog}</button>
      </div>
      <pre class="max-h-80 overflow-auto whitespace-pre-wrap break-all p-4 font-mono text-[11px] leading-relaxed text-neutral-600 dark:text-neutral-400">{output || m.hint}</pre>
    </section>
  </div>
</div>
