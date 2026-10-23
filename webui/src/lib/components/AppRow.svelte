<script lang="ts">
  import { SlidersHorizontal } from "lucide-svelte"
  import AppIcon from "./AppIcon.svelte"
  import OverflowText from "./OverflowText.svelte"
  import type { TargetMode } from "../targets"

  let { packageName, label, icon = null, selected, mode = "auto", modeLabel = "", system = false,
    systemLabel = "", customPolicy = false, policyLabel = "", missing = false, missingLabel = "", editLabel,
    disabled = false, ontoggle, ontune }: {
    packageName: string; label: string; icon?: string | null; selected: boolean; mode?: TargetMode; modeLabel?: string
    system?: boolean; systemLabel?: string; customPolicy?: boolean; policyLabel?: string; missing?: boolean
    missingLabel?: string; editLabel: string; disabled?: boolean; ontoggle: () => void; ontune?: () => void
  } = $props()
</script>

<div class="app-row flex min-h-16 items-center gap-1 px-2 py-2" class:is-selected={selected}>
  <button type="button" class="flex min-w-0 flex-1 items-center gap-2 text-left disabled:opacity-50"
    {disabled} aria-label={label + " · " + packageName} aria-pressed={selected} onclick={ontoggle}
    oncontextmenu={(event) => { if (!disabled && selected && ontune) { event.preventDefault(); ontune() } }}>
    <AppIcon {label} {icon} />
    <span class="grid min-w-0 flex-1 gap-0.5">
      <span class="app-row-label text-xs font-medium leading-4 [overflow-wrap:anywhere]">{label}</span>
      <OverflowText text={packageName} />
      {#if (selected && mode !== "auto") || system || (selected && customPolicy) || missing}
        <span class="flex flex-wrap gap-1 text-[10px] leading-4 text-[var(--muted)]">
          {#if selected && mode !== "auto"}<span>{mode === "generate" ? "!" : "?"} {modeLabel}</span>{/if}
          {#if selected && customPolicy}<span>{policyLabel}</span>{/if}
          {#if system}<span>{systemLabel}</span>{/if}
          {#if missing}<span>{missingLabel}</span>{/if}
        </span>
      {/if}
    </span>
  </button>
  {#if ontune}
    <span class="grid size-8 shrink-0 place-items-center">
      <button type="button" class="target-tune" class:is-visible={selected}
        disabled={disabled || !selected} aria-hidden={!selected} tabindex={selected ? 0 : -1}
        aria-label={editLabel + " · " + packageName} onclick={ontune}><SlidersHorizontal size={16} /></button>
    </span>
  {/if}
  <label class="check-hit">
    <input type="checkbox" class="ui-check" checked={selected} {disabled}
      aria-label={label + " · " + packageName} onchange={ontoggle} />
  </label>
</div>

<style>
  .app-row {
    border-radius: 12px;
    background: var(--panel);
    box-shadow: inset 0 0 0 1px transparent;
    transition: background-color 180ms ease-out, box-shadow 180ms ease-out;
  }
  .app-row.is-selected {
    background: var(--accent-bg);
    box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--accent) 24%, transparent);
  }
  .app-row-label { transition: color 180ms ease-out; }
  .app-row.is-selected .app-row-label { color: var(--accent); }
  .target-tune {
    display: grid;
    width: 32px;
    height: 32px;
    place-items: center;
    border-radius: 8px;
    color: var(--accent);
    opacity: 0;
    visibility: hidden;
    transform: scale(.9);
    pointer-events: none;
    transition: opacity 150ms ease-out, transform 150ms ease-out, background-color 150ms, visibility 0s 150ms;
  }
  .target-tune.is-visible {
    opacity: 1;
    visibility: visible;
    transform: scale(1);
    pointer-events: auto;
    transition-delay: 0s;
  }
  .target-tune:disabled { opacity: .45; }
  .target-tune[aria-hidden="true"] { opacity: 0; }
  .target-tune:active:not(:disabled) { transform: scale(.95); }
  @media (hover: hover) {
    .target-tune:hover:not(:disabled) { background: var(--panel); }
  }
  @media (prefers-reduced-motion: reduce) {
    .app-row, .app-row-label, .target-tune { transition: none; }
  }
</style>
