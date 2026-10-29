<script lang="ts">
  import { onDestroy, untrack } from "svelte"
  import { FileUp, History } from "lucide-svelte"
  import type { Locale } from "../i18n"
  import { KeyboxFilesClient, KeyboxFilesError, canConfirm, type KeyboxPreview, type PreparedImport } from "../keyboxFiles"
  import { keyboxFilesMessages } from "../keyboxFilesMessages"
  import { ui } from "../ui"
  import Modal from "./Modal.svelte"
  import BusyIndicator from "./BusyIndicator.svelte"

  interface Props {
    bin: string
    moddir: string
    lang: Locale
    disabled?: boolean
    running?: "install" | "restore" | null
    onInstall: (operation: "install" | "restore", id: string, revision: string) => Promise<void>
  }
  let { bin, moddir, lang, disabled = false, running = null, onInstall }: Props = $props()
  let m = $derived(keyboxFilesMessages(lang))
  type Mode = "import" | "backups"
  interface Task {
    client: KeyboxFilesClient
    controller: AbortController
    prepared?: PreparedImport
    applying: boolean
    retired: boolean
    mayContinue: boolean
  }
  let task: Task | undefined
  let disposed = false
  let modal = $state<Mode | null>(null)
  let mode = $state<Mode>("import")
  let loading = $state(false)
  let applying = $state(false)
  let pendingOperation = $derived(running ?? (applying ? (mode === "import" ? "install" : "restore") : null))
  let preview = $state<KeyboxPreview | null>(null)
  let backups = $state<KeyboxPreview[]>([])
  let error = $state("")
  let previousBin = untrack(() => bin)
  let previousModdir = untrack(() => moddir)
  let previousDisabled = untrack(() => disabled)

  function current(candidate: Task) { return !disposed && task === candidate && !candidate.retired }
  function retire() {
    const old = task
    task = undefined
    if (!old) return
    old.retired = true
    // The parent owns the running install. Its input must survive component disposal.
    if (!old.applying) {
      old.controller.abort()
      if (!old.mayContinue) void old.prepared?.discard()
    }
  }
  function reset() {
    retire()
    modal = null
    preview = null
    backups = []
    loading = false
    error = ""
  }
  $effect(() => {
    const nextBin = bin, nextModdir = moddir, nextDisabled = disabled
    untrack(() => {
      const contextChanged = nextBin !== previousBin || nextModdir !== previousModdir
      const becameDisabled = nextDisabled && !previousDisabled
      previousBin = nextBin
      previousModdir = nextModdir
      previousDisabled = nextDisabled
      if (contextChanged || (becameDisabled && !applying)) reset()
    })
  })
  onDestroy(() => { disposed = true; retire() })

  function begin(): Task {
    retire()
    const next: Task = { client: new KeyboxFilesClient(bin, moddir), controller: new AbortController(), applying: false, retired: false, mayContinue: false }
    task = next
    return next
  }
  function close() {
    if (applying) return
    reset()
  }
  function openImport() {
    if (disabled || pendingOperation || modal) return
    reset()
    mode = "import"
    modal = "import"
    begin()
  }
  async function openBackups() {
    if (disabled || pendingOperation || modal) return
    reset()
    mode = "backups"
    modal = "backups"
    await readBackups()
  }
  async function readBackups() {
    if (pendingOperation || disabled) return
    const next = begin()
    loading = true
    error = ""
    preview = null
    try {
      const list = await next.client.backups()
      if (current(next)) backups = list.backups
    } catch {
      if (current(next)) error = m.listFailed
    } finally {
      if (current(next)) loading = false
    }
  }
  async function choose(event: Event) {
    const input = event.currentTarget as HTMLInputElement
    const file = input.files?.[0]
    input.value = ""
    if (!file || disabled || pendingOperation || modal !== "import") return
    const next = begin()
    loading = true
    preview = null
    error = ""
    try {
      const prepared = await next.client.upload(file, next.controller.signal)
      if (!current(next)) { await prepared.discard(); return }
      next.prepared = prepared
      preview = prepared.preview
    } catch (failure) {
      if (!current(next)) return
      const code = failure instanceof KeyboxFilesError ? failure.code : "upload"
      error = code === "empty" ? m.empty : code === "tooLarge" ? m.tooLarge : code === "extension" ? m.extension :
        code === "read" ? m.readFailed : m.uploadFailed
    } finally {
      if (current(next)) loading = false
    }
  }
  async function inspectBackup(backup: KeyboxPreview) {
    if (loading || disabled || pendingOperation) return
    const next = begin()
    loading = true
    error = ""
    try {
      const result = await next.client.inspect(backup.id, backup.target)
      if (result.revision !== backup.revision) throw new KeyboxFilesError("protocol")
      if (current(next)) preview = result
    } catch {
      if (current(next)) error = m.previewFailed
    } finally {
      if (current(next)) loading = false
    }
  }
  async function install() {
    const next = task
    const selected = preview
    if (!next || !current(next) || disabled || pendingOperation || loading || !canConfirm(selected)) return
    const operation = mode === "import" ? "install" : "restore"
    next.applying = true
    applying = true
    error = ""
    modal = null
    let completed = false
    try {
      await onInstall(operation, selected!.id, selected!.revision)
      completed = true
      if (current(next)) {
        preview = null
      }
    } catch (failure) {
      // A disposed or failed monitor does not prove that the native install
      // stopped. Let Go remove the session on success, or expire it on failure.
      if (failure instanceof KeyboxFilesError && failure.code === "detached") next.mayContinue = true
      if (current(next)) {
        error = next.mayContinue ? m.operationUnknown : operation === "install" ? m.installFailed : m.restoreFailed
        modal = mode
      }
    } finally {
      next.applying = false
      // Failed installs keep their preview/session for retry until the user closes it.
      if (completed || !current(next)) {
        if (completed || !next.mayContinue) await next.prepared?.discard()
        if (task === next) task = undefined
      }
      if (!disposed) applying = false
    }
  }
  function status(value: string | undefined) {
    return value === "valid" ? m.valid : value === "invalid" ? m.invalid : value === "expired" ? m.expired :
      value === "not_yet_valid" ? m.notYetValid : m.unknown
  }
  function statusClass(value: string | undefined) {
    return value === "valid" ? "success" : value === "invalid" || value === "expired" || value === "not_yet_valid" ? "failure" : "muted"
  }
  function date(seconds: number, part: "date" | "time") {
    const value = new Date(seconds * 1000)
    return Number.isNaN(value.getTime()) ? m.unknown : part === "date" ?
      value.toLocaleDateString(lang, { year: "numeric", month: "2-digit", day: "2-digit" }) :
      value.toLocaleTimeString(lang, { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false })
  }
  function size(bytes: number) {
    return bytes < 1024 ? bytes + " B" : (bytes / 1024).toFixed(1) + " KiB"
  }
</script>

{#snippet dateTime(seconds: number)}
  <time class="date-time"><span>{date(seconds, "date")}</span><span>{date(seconds, "time")}</span></time>
{/snippet}

<div class="keybox-files">
  <div class="actions" class:working={!!pendingOperation}>
    <button type="button" class={ui.compactButton} disabled={disabled || !!pendingOperation} onclick={openImport}><FileUp size={13} />{m.import}</button>
    <button type="button" class={ui.compactButton} disabled={disabled || !!pendingOperation} onclick={openBackups}><History size={13} />{m.backups}</button>
    {#if pendingOperation}
      <span class="operation"><BusyIndicator active={true} visible={true} label={pendingOperation === "install" ? m.installing : m.restoring} /></span>
    {/if}
  </div>
  {#if error && !modal}<p class="failure" role="alert">{error}</p>{/if}
</div>

{#if modal}
  <Modal title={modal === "import" ? m.import : m.backups} closeLabel={m.close} onclose={close}>
    {#if modal === "import"}
      <label class={ui.compactButton + " file-picker"} class:disabled={disabled || !!pendingOperation}>
        <FileUp size={14} />{m.choose}
        <input type="file" accept="*/*" aria-label={m.choose} onchange={choose}
          oncancel={(event) => { event.stopPropagation(); if (!applying) { retire(); preview = null; loading = false; error = "" } }}
          disabled={disabled || !!pendingOperation} />
      </label>
    {:else if !preview}
      {#if !loading && !error && backups.length === 0}<p class="hint">{m.emptyList}</p>{/if}
      <div class="backup-list">
        {#each backups as backup (backup.id)}
          <button type="button" class="backup glass-control" disabled={loading || disabled || !!pendingOperation} onclick={() => inspectBackup(backup)}>
            <span class="backup-heading"><span>{@render dateTime(backup.modified)}</span><span>{size(backup.size)}</span></span>
            <span class="serials">{backup.serials.length ? backup.serials.slice(0, 2).join(", ") : m.noCertificates}{backup.serials.length > 2 ? "…" : ""}</span>
            <span class="muted">{m.integrity}: <span class={statusClass(backup.integrity?.status)}>{status(backup.integrity?.status)}</span> · <span class={backup.compatible ? "success" : "failure"}>{backup.compatible ? m.compatible : m.incompatible}</span></span>
          </button>
        {/each}
      </div>
      {#if error}<button type="button" class={ui.compactButton} disabled={loading || disabled} onclick={readBackups}>{m.retry}</button>{/if}
    {/if}
    {#if loading}<BusyIndicator active={true} visible={true} label={modal === "import" ? m.uploading : m.loading} />{/if}
    {#if error}<p class="failure" role="alert">{error}</p>{/if}
    {#if preview}
      <dl class="preview">
        <div><dt>{m.currentTarget}</dt><dd>{preview.backend.name || preview.backend.identity}<br /><span class="font-mono text-[11px]">{preview.target}</span></dd></div>
        <div><dt>{m.size}</dt><dd>{size(preview.size)}</dd></div>
        <div><dt>{m.modified}</dt><dd>{@render dateTime(preview.modified)}</dd></div>
        <div><dt>{m.structure}</dt><dd class={preview.structure_valid ? "success" : "failure"}>{preview.structure_valid ? m.valid : m.invalid}</dd></div>
        <div><dt>{m.certificates}</dt><dd class="serials">{preview.serials.length ? preview.serials.join(", ") : m.noCertificates}</dd></div>
        <div><dt>{m.integrity}</dt><dd class={statusClass(preview.integrity?.status)}>{status(preview.integrity?.status)}</dd></div>
        <div><dt>{m.algorithms}</dt><dd>
          {#if preview.integrity?.keys.length}
            {#each preview.integrity.keys as key, index}
              {@const valid = key.chain_valid && key.key_match && key.private_key_valid}
              {#if index}, {/if}{key.algorithm.toUpperCase()} / {key.root} / <span class={valid ? "success" : "failure"}>{valid ? m.valid : m.invalid}</span>
            {/each}
          {:else}<span class="muted">{m.unknown}</span>{/if}
        </dd></div>
        <div><dt>{m.validity}</dt><dd class={statusClass(preview.validity?.status)}>{status(preview.validity?.status)}</dd></div>
        {#each preview.validity?.certificates ?? [] as certificate}
          <div class="certificate">
            <dt class="serials">{certificate.serial}</dt>
            <dd class="certificate-range">
              {@render dateTime(certificate.not_before)}<span>–</span>{@render dateTime(certificate.not_after)}
              <span class={"certificate-status " + statusClass(certificate.status)}>{status(certificate.status)}</span>
            </dd>
          </div>
        {/each}
        <div><dt>{m.compatibility}</dt><dd class={preview.compatible ? "success" : "failure"}>{preview.compatible ? m.compatible : m.incompatible}</dd></div>
      </dl>
      <p class="hint">{m.recheck}</p>
      {#if !canConfirm(preview)}<p class="failure">{m.blocked}</p>{/if}
    {/if}
    {#snippet footer(requestClose)}
      <button type="button" class={ui.compactButton} onclick={requestClose}>{m.cancel}</button>
      {#if preview}
        <button type="button" class={ui.compactButton} disabled={disabled || !!pendingOperation || loading || !canConfirm(preview)} onclick={install}>
          {mode === "import" ? m.install : m.restore}
        </button>
      {/if}
    {/snippet}
  </Modal>
{/if}

<style>
  .keybox-files { min-width: 0; padding-top: 8px; }
  .actions { position: relative; display: flex; flex-wrap: wrap; align-items: center; gap: 6px 8px; }
  .actions.working > button { opacity: 0; }
  .operation { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; pointer-events: none; }
  p { margin: 4px 0; font-size: 12px; overflow-wrap: anywhere; }
  .hint, .muted { color: var(--muted); }
  .success { color: var(--success); }
  .failure { color: var(--red); }
  .file-picker { position: relative; max-width: 100%; margin-top: 4px; overflow: hidden; }
  .file-picker.disabled { opacity: .5; }
  .file-picker:not(.disabled):hover { border-color: var(--accent); background: var(--accent-bg); }
  .file-picker:not(.disabled):active { transform: scale(.98); }
  .file-picker:focus-within { outline: 2px solid var(--accent); outline-offset: 2px; }
  .file-picker input { position: absolute; inset: 0; width: 100%; height: 100%; opacity: 0; cursor: pointer; }
  .file-picker input:disabled { cursor: default; }
  .backup-list { display: grid; gap: 6px; margin-top: 6px; }
  .backup { display: grid; width: 100%; gap: 2px; border: 1px solid var(--line); border-radius: 6px; background: var(--inset); color: var(--text); padding: 6px 8px; text-align: start; font-size: 11px; overflow-wrap: anywhere; }
  .backup:hover:enabled { border-color: var(--accent); }
  .backup:disabled { opacity: .48; }
  .backup-heading { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 4px 8px; font-size: 12px; }
  .preview { margin: 8px 0; font-size: 12px; }
  .preview > div { display: grid; grid-template-columns: minmax(0, 2fr) minmax(0, 3fr); align-items: start; gap: 2px 8px; padding: 4px 0; border-bottom: 1px solid var(--line); }
  dt { min-width: 0; color: var(--muted); overflow-wrap: anywhere; }
  dd { margin: 0; text-align: end; min-width: 0; overflow-wrap: anywhere; }
  .preview > .certificate { grid-template-columns: minmax(0, 1fr); gap: 4px; }
  .certificate-range { display: flex; flex-wrap: wrap; justify-content: flex-start; align-items: baseline; gap: 2px 6px; text-align: start; }
  .date-time { display: inline-flex; flex-wrap: wrap; justify-content: flex-end; gap: 0 4px; font-variant-numeric: tabular-nums; }
  .date-time span, .certificate-status { white-space: nowrap; overflow-wrap: normal; }
  .certificate-range .date-time { justify-content: flex-start; }
  .serials { overflow-wrap: anywhere; font-variant-numeric: tabular-nums; }
</style>
