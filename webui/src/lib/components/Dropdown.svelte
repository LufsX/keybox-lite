<script lang="ts" generics="Value extends string">
  import type { Snippet } from "svelte"
  import { fade } from "svelte/transition"
  import { Check } from "lucide-svelte"
  import { ui } from "../ui"

  interface Props {
    id: string
    label: string
    options: readonly { value: Value; label: string; lang?: string }[]
    selected: Value
    open: boolean
    reducedMotion?: boolean
    onOpenChange: (open: boolean) => void
    onSelect: (value: Value) => void
    trigger: Snippet
    optionIcon?: Snippet<[Value]>
  }

  let { id, label, options, selected, open, reducedMotion = false, onOpenChange, onSelect, trigger, optionIcon }: Props = $props()
  let control: HTMLDivElement
  let button: HTMLButtonElement

  function dismiss(event: Event) {
    if (open && event.target instanceof Node && !control?.contains(event.target)) onOpenChange(false)
  }
  function keydown(event: KeyboardEvent) {
    if (event.key === "Escape" && open) {
      onOpenChange(false)
      button?.focus()
    }
  }
  function select(value: Value) {
    onSelect(value)
    onOpenChange(false)
    button?.focus()
  }
</script>

<svelte:window onpointerdown={dismiss} onfocusin={dismiss} onkeydown={keydown} />

<div class="relative" bind:this={control}>
  <button class={ui.iconButton} bind:this={button} onclick={() => onOpenChange(!open)} title={label} aria-label={label} aria-expanded={open} aria-controls={id}>
    {@render trigger()}
  </button>
  {#if open}
    <div {id} class={ui.menu + " max-h-[min(320px,60dvh)] overflow-y-auto overscroll-contain"} role="group" aria-label={label} in:fade={{ duration: reducedMotion ? 0 : 140 }}>
      {#each options as option}
        <button class={ui.menuItem + (selected === option.value ? " " + ui.menuItemActive : "")} onclick={() => select(option.value)} aria-pressed={selected === option.value}>
          {#if optionIcon}{@render optionIcon(option.value)}{/if}
          <span lang={option.lang}>{option.label}</span>
          {#if selected === option.value}<Check size={14} />{/if}
        </button>
      {/each}
    </div>
  {/if}
</div>
