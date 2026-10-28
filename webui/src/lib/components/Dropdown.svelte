<script lang="ts" generics="Value extends string">
  import { tick, type Snippet } from "svelte"
  import { surface } from "../motion"
  import { Check, ChevronDown } from "lucide-svelte"
  import { floatingDropdown } from "../dropdownPosition"
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
    trigger?: Snippet
    optionIcon?: Snippet<[Value]>
  }

  let { id, label, options, selected, open, reducedMotion = false, disabled = false, describedBy,
    triggerClass = ui.iconButton, onOpenChange, onSelect, trigger, optionIcon }: Props = $props()
  let control: HTMLDivElement
  let button = $state<HTMLButtonElement>(null!)
  let menu = $state<HTMLDivElement>(null!)

  function dismiss(event: Event) {
    if (open && event.target instanceof Node && !control?.contains(event.target) && !menu?.contains(event.target)) onOpenChange(false)
  }
  function keydown(event: KeyboardEvent) {
    if (event.key === "Escape" && open) {
      event.preventDefault()
      event.stopImmediatePropagation()
      onOpenChange(false)
      button?.focus({ preventScroll: true })
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
      await new Promise<void>(resolve => requestAnimationFrame(() => resolve()))
    }
    if (!open || disabled) return
    const items = Array.from(menu?.querySelectorAll<HTMLButtonElement>("[data-option]") ?? [])
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

<svelte:window onpointerdown={dismiss} onfocusin={dismiss} onkeydowncapture={keydown} />

<div class="relative" bind:this={control}>
  <button type="button" class={triggerClass} bind:this={button} {disabled} onclick={() => onOpenChange(!open)} onkeydown={navigate}
    title={label} aria-label={label} aria-describedby={describedBy} aria-expanded={open && !disabled} aria-controls={id}>
    {#if trigger}{@render trigger()}{:else}<span>{options.find(option => option.value === selected)?.label ?? selected}</span><ChevronDown size={14} />{/if}
  </button>
  {#if open && !disabled}
    <div {id} bind:this={menu} use:floatingDropdown={button} class={ui.menu + " dropdown-menu overflow-y-auto overscroll-contain"} role="group" aria-label={label} in:surface={{ duration: reducedMotion ? 0 : 160 }}>
      {#each options as option}
        <button type="button" data-option class={ui.menuItem + (selected === option.value ? " " + ui.menuItemActive : "")} onclick={() => select(option.value)} onkeydown={navigate} aria-pressed={selected === option.value}>
          {#if optionIcon}{@render optionIcon(option.value)}{/if}
          <span lang={option.lang}>{option.label}</span>
          <span class="grid size-[14px] !flex-none place-items-center">
            {#if selected === option.value}<span class="grid" in:surface={{ y: 0 }}><Check size={14} /></span>{/if}
          </span>
        </button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .dropdown-menu {
    position: fixed;
    inset: auto;
    margin: 0;
    box-sizing: border-box;
    max-width: none;
    color: var(--text);
  }
</style>
