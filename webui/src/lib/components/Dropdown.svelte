<script lang="ts" generics="Value extends string">
  import { tick, type Snippet } from "svelte"
  import { surface } from "../motion"
  import { Check } from "lucide-svelte"
  import { ui } from "../ui"

  interface Props {
    id: string
    label: string
    options: readonly { value: Value; label: string; lang?: string }[]
    selected: Value
    open: boolean
    reducedMotion?: boolean
    disabled?: boolean
    describedBy?: string
    triggerClass?: string
    onOpenChange: (open: boolean) => void
    onSelect: (value: Value) => void
    trigger: Snippet
    optionIcon?: Snippet<[Value]>
  }

  let { id, label, options, selected, open, reducedMotion = false, disabled = false, describedBy,
    triggerClass = ui.iconButton, onOpenChange, onSelect, trigger, optionIcon }: Props = $props()
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
  async function navigate(event: KeyboardEvent) {
    if (disabled) return
    const keys = ["ArrowDown", "ArrowUp", "Home", "End"]
    if (!keys.includes(event.key)) return
    event.preventDefault()
    if (!open) {
      onOpenChange(true)
      await tick()
    }
    const items = Array.from(control.querySelectorAll<HTMLButtonElement>("[data-option]"))
    if (!items.length) return
    const index = items.indexOf(document.activeElement as HTMLButtonElement)
    const next = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 :
      index < 0 ? (event.key === "ArrowUp" ? items.length - 1 : Math.max(0, options.findIndex(option => option.value === selected))) :
      (index + (event.key === "ArrowUp" ? -1 : 1) + items.length) % items.length
    items[next]?.focus()
  }
  function select(value: Value) {
    if (disabled) return
    onSelect(value)
    onOpenChange(false)
    button?.focus()
  }
</script>

<svelte:window onpointerdown={dismiss} onfocusin={dismiss} onkeydown={keydown} />

<div class="relative" bind:this={control}>
  <button type="button" class={triggerClass} bind:this={button} {disabled} onclick={() => onOpenChange(!open)} onkeydown={navigate}
    title={label} aria-label={label} aria-describedby={describedBy} aria-expanded={open && !disabled} aria-controls={id}>
    {@render trigger()}
  </button>
  {#if open && !disabled}
    <div {id} class={ui.menu + " max-h-[min(320px,60dvh)] overflow-y-auto overscroll-contain"} role="group" aria-label={label} transition:surface={{ duration: reducedMotion ? 0 : 160 }}>
      {#each options as option}
        <button type="button" data-option class={ui.menuItem + (selected === option.value ? " " + ui.menuItemActive : "")} onclick={() => select(option.value)} onkeydown={navigate} aria-pressed={selected === option.value}>
          {#if optionIcon}{@render optionIcon(option.value)}{/if}
          <span lang={option.lang}>{option.label}</span>
          <span class="grid size-[14px] !flex-none place-items-center">
            {#if selected === option.value}<span class="grid" transition:surface={{ y: 0 }}><Check size={14} /></span>{/if}
          </span>
        </button>
      {/each}
    </div>
  {/if}
</div>
