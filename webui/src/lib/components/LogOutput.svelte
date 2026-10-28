<script lang="ts">
  import { Terminal, RefreshCw, Loader2 } from "lucide-svelte"
  import { surface } from "../motion"
  import type { LogLine, LogTone } from "../log"
  import { ui } from "../ui"

  interface Props {
    output: string
    lines: LogLine[]
    title: string
    hint: string
    currentLabel: string
    savedLabel: string
    refreshLabel: string
    loadingLabel: string
    saved: boolean
    loading: boolean
    disabled?: boolean
    onCurrent: () => void
    onSaved: () => void
  }

  let { output, lines, title, hint, currentLabel, savedLabel, refreshLabel, loadingLabel,
    saved, loading, disabled = false, onCurrent, onSaved }: Props = $props()
  const id = $props.id()

  const toneClasses: Record<LogTone, string> = {
    stage: "font-[650] text-[var(--accent)]",
    success: "font-[650] text-[var(--success)]",
    failure: "font-[650] text-[var(--log-failure)]",
    waiting: "font-[650] text-[var(--log-waiting)]",
    source: "font-[550] text-[var(--log-source)]",
  }
</script>

<section class={ui.panel}>
  <div class={ui.heading}>
    <h2 class={ui.headingTitle}><Terminal size={17} />{title}</h2>
    <span role="status" class="flex items-center gap-1 text-[11px] text-[var(--muted)]">
      {#if loading}<Loader2 size={13} class="motion-safe:animate-spin" />{loadingLabel}{/if}
    </span>
  </div>
  <div class="mb-2 flex items-center gap-1">
    <div class="log-views" role="group" aria-label={title}>
      <button type="button" aria-pressed={!saved} aria-controls={id} onclick={onCurrent}>{currentLabel}</button>
      <button type="button" aria-pressed={saved} aria-controls={id} disabled={disabled || loading}
        onclick={onSaved}>{savedLabel}</button>
    </div>
    {#if saved}
      <button type="button" class="log-refresh" aria-label={refreshLabel} title={refreshLabel}
        disabled={disabled || loading} onclick={onSaved}><RefreshCw size={14} /></button>
    {/if}
  </div>
  {#key saved}
    <pre {id} aria-label={saved ? savedLabel : currentLabel} aria-busy={loading}
      class="m-0 max-h-[300px] overflow-y-auto whitespace-pre-wrap rounded-lg bg-[var(--inset)] p-2 font-mono text-[11px] leading-normal text-[var(--text)] [overflow-wrap:anywhere]"
      in:surface={{ y: 0, scale: 1, duration: 120 }}>{#if output}{#each lines as line (line)}{#each line.parts as part}<span class={part.tone ? toneClasses[part.tone] : undefined}>{part.text}</span>{/each}{/each}{:else}{hint}{/if}</pre>
  {/key}
</section>

<style>
  .log-views { display: flex; flex-wrap: wrap; gap: 2px; min-width: 0; padding: 2px; border-radius: 6px; background: var(--inset); }
  .log-views button { min-height: 28px; padding: 3px 8px; border-radius: 4px; font-size: 11px; color: var(--muted); overflow-wrap: anywhere; transition: color 150ms, background-color 150ms; }
  .log-views button[aria-pressed="true"] { background: var(--panel); color: var(--accent); font-weight: 550; }
  .log-views button:disabled, .log-refresh:disabled { opacity: .5; }
  .log-refresh { display: grid; flex-shrink: 0; width: 28px; height: 28px; place-items: center; border-radius: 6px; color: var(--muted); }
  .log-refresh:active:not(:disabled) { background: var(--inset); }
</style>
