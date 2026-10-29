<script lang="ts">
  import { onDestroy } from "svelte"
  import { ClipboardPaste, Loader2 } from "lucide-svelte"
  import Modal from "./Modal.svelte"
  import TargetTransferNames from "./TargetTransferNames.svelte"
  import { ui } from "../ui"
  import type { Locale } from "../i18n"
  import type { TargetDraft, TargetStatus } from "../targets"
  import { saveConfiguration } from "../configDownload"
  import { resolveTargetTransferMessages } from "../targetTransferMessages"
  import {
    exportTargetTransfer, parseTargetTransfer, readTargetTransfer, previewTargetTransfer,
    targetTransferError, TargetTransferError, type TransferPreview,
  } from "../targetTransfer"

  let { mode, lang, draft, status, dirty, context, onclose, onapply, onNotice }: {
    mode: "import" | "export"; lang: Locale; draft: TargetDraft; status: TargetStatus; dirty: boolean;
    context: string; onclose: () => void; onapply: (draft: TargetDraft) => boolean; onNotice?: (message: string) => void;
  } = $props()
  let m = $derived(resolveTargetTransferMessages(lang))
  let preview = $state<TransferPreview | null>(null)
  let reading = $state(false)
  let saving = $state(false)
  let error = $state("")
  let text = $state("")
  let textarea = $state<HTMLTextAreaElement>()
  let sequence = 0
  let disposed = false
  let closing = false
  let controller: AbortController | undefined
  const initialContext = (() => context)()
  const initialDraft = (() => JSON.stringify(draft))()
  const inlineNamesLimit = 30
  const filename = "keybox-lite-targets-" + new Date().toISOString().replace(/[:.]/g, "-") + ".json"
  const inputClass = "min-h-8 min-w-0 rounded-lg border border-[var(--line)] bg-[var(--inset)] px-2 py-1 text-xs text-[var(--text)]"
  const live = (id: number) => !disposed && !closing && sequence === id && context === initialContext && JSON.stringify(draft) === initialDraft
  function cancelWork() { closing = true; sequence++; controller?.abort(); controller = undefined }
  function close() { cancelWork(); onclose() }
  onDestroy(() => { disposed = true; cancelWork() })
  $effect(() => {
    if (mode === "export") {
      try { text = exportTargetTransfer(draft, status.backend!) }
      catch (cause) { error = m.errors[targetTransferError(cause)] }
    }
  })
  function previewText(value: string) {
    text = value
    preview = null
    error = ""
    if (!value.trim()) return
    try { preview = previewTargetTransfer(parseTargetTransfer(value, draft), draft, status) }
    catch (cause) { error = m.errors[targetTransferError(cause)] }
  }
  function editText(event: Event) {
    if (reading || !live(sequence)) return
    sequence++
    previewText((event.currentTarget as HTMLTextAreaElement).value)
  }
  async function paste() {
    if (reading || !live(sequence)) return
    const id = ++sequence
    reading = true
    error = ""
    try {
      const value = await navigator.clipboard.readText()
      if (live(id)) previewText(value)
    } catch {
      if (live(id)) error = m.pasteFailed
    } finally { if (live(id)) reading = false }
  }
  async function choose(event: Event) {
    const input = event.currentTarget as HTMLInputElement
    const file = input.files?.[0]
    input.value = ""
    if (!file || reading || !live(sequence)) return
    const id = ++sequence
    reading = true
    text = ""
    preview = null
    error = ""
    try {
      const value = await readTargetTransfer(file, draft)
      if (!live(id)) return
      text = JSON.stringify(value)
      preview = previewTargetTransfer(value, draft, status)
    } catch (cause) {
      if (live(id)) error = cause instanceof TargetTransferError ? m.errors[cause.code] : m.readFailed
    } finally { if (live(id)) reading = false }
  }
  function apply(requestClose: () => void) {
    if (closing || reading || !preview || !live(sequence)) return
    if (onapply(preview.draft)) { cancelWork(); requestClose() }
  }
  function selectText() { textarea?.focus({ preventScroll: true }); textarea?.select() }
  async function copy() {
    if (!text || saving || !live(sequence)) return
    const id = ++sequence
    error = ""
    try {
      await navigator.clipboard.writeText(text)
      if (live(id)) onNotice?.(m.copied)
    } catch {
      if (live(id)) { error = m.copyFailed; selectText() }
    }
  }
  async function download() {
    if (!text || saving || !live(sequence)) return
    const id = ++sequence
    controller?.abort()
    controller = new AbortController()
    saving = true
    error = ""
    try {
      const value = await saveConfiguration(filename, text, controller.signal)
      if (live(id)) onNotice?.(value.path ? m.savedAt + ": " + value.path : m.downloaded)
    } catch {
      if (live(id)) error = m.downloadFailed
    } finally { if (live(id)) { saving = false; controller = undefined } }
  }
</script>

<Modal title={mode === "import" ? m.import : m.export} closeLabel={m.close} onclose={close}>
  <div class="grid min-w-0 gap-2">
    {#if mode === "export"}
      {#if dirty}<p class="text-[var(--muted)]">{m.draftExport}</p>{/if}
      <textarea bind:this={textarea} class={inputClass + " w-full font-mono text-[11px]"} rows="6"
        aria-label={m.jsonLabel} readonly value={text} spellcheck="false"></textarea>
      <button type="button" class={ui.compactButton + " justify-self-start"} onclick={selectText}>{m.select}</button>
    {:else}
      <div class="flex flex-wrap items-center gap-2 pt-1">
        <label class={ui.compactButton + " file-picker relative overflow-hidden focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-[var(--accent)]"}
          class:disabled={reading}>
          {m.choose}
          <input type="file" accept="*/*" aria-label={m.choose} disabled={reading}
            class="absolute inset-0 size-full cursor-pointer opacity-0 disabled:cursor-default" onchange={choose} />
        </label>
        <button type="button" class={ui.compactButton} disabled={reading} onclick={paste}>{#if reading}<Loader2 size={13} class="motion-safe:animate-spin" />{:else}<ClipboardPaste size={13} />{/if}{m.paste}</button>
      </div>
      <textarea bind:this={textarea} class={inputClass + " w-full font-mono text-[11px]"} rows="5"
        aria-label={m.jsonLabel} placeholder={m.jsonLabel} value={text} oninput={editText}
        disabled={reading} spellcheck="false" autocapitalize="off"></textarea>
      {#if preview}
        <div class="grid gap-1" data-transfer-preview>
          <p class="font-semibold">{m.preview}</p>
          {#if preview.source.identity !== preview.destination.identity || preview.source.backend !== preview.destination.backend}
            <p class="text-[var(--muted)]">{m.source}: {preview.source.identity} → {preview.destination.identity}</p>
          {/if}
          <dl class="grid grid-cols-2 gap-x-3 gap-y-1 rounded-md border border-[var(--line)] p-2">
            {#each Object.entries(preview.changes) as [key, count]}
              <div class="flex items-start justify-between gap-2" data-change={key}>
                <dt>{m.changes[key as keyof typeof m.changes]}</dt><dd class="font-mono">{count}</dd>
              </div>
            {/each}
          </dl>
          {#if !Object.values(preview.changes).some(Boolean)}<p>{m.noChanges}</p>{/if}
          {#if preview.warnings.length}
            {#each preview.warnings as warning}
              <div class="mt-1 rounded-md border border-[var(--line)] p-2" data-warning={warning.code}>
                <p>{m.warnings[warning.code]} ({warning.count})</p>
                {#if warning.names.length > inlineNamesLimit}
                  <TargetTransferNames names={warning.names} label={m.warnings[warning.code]} />
                {:else if warning.names.length}
                  <p class="mt-1 break-all font-mono text-[11px] text-[var(--muted)]">{warning.names.join(", ")}</p>
                {/if}
              </div>
            {/each}
          {/if}
          {#if preview.missing.length}
            <details class="mt-1" open>
              <summary>{m.missing} ({preview.missing.length})</summary>
              {#if preview.missing.length > inlineNamesLimit}
                <div data-missing><TargetTransferNames names={preview.missing} label={m.missing} /></div>
              {:else}
                <ul class="mt-1 grid gap-0.5 break-all font-mono text-[11px]" data-missing>
                  {#each preview.missing as name}<li>{name}</li>{/each}
                </ul>
              {/if}
            </details>
          {/if}
          <p class="mt-1 text-[var(--muted)]">{m.draftOnly}</p>
        </div>
      {/if}
    {/if}
    {#if error}<p role="alert" class="whitespace-pre-wrap text-[var(--red)]">{error}</p>{/if}
  </div>
  {#snippet footer(requestClose)}
    <button type="button" class={ui.compactButton}
      onclick={() => { cancelWork(); requestClose() }}>{mode === "import" ? m.cancel : m.close}</button>
    {#if mode === "export"}
      <button type="button" class={ui.compactButton} disabled={!text || saving} onclick={copy}>{m.copy}</button>
      <button type="button" class={ui.compactButton}
        disabled={!text || saving} aria-busy={saving} onclick={download}>{m.download}</button>
    {:else}
      <button type="button" class={ui.compactButton}
        disabled={!preview || reading} onclick={() => apply(requestClose)}>{m.confirm}</button>
    {/if}
  {/snippet}
</Modal>

<style>
  .file-picker.disabled { opacity: .5; }
  .file-picker:not(.disabled):hover { border-color: var(--accent); background: var(--accent-bg); }
  .file-picker:not(.disabled):active { transform: scale(.98); }
</style>
