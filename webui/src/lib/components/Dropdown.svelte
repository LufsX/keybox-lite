<script lang="ts" generics="Value extends string">
  import { onMount, tick, type Snippet } from "svelte"
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
  let systemReducedMotion = $state(false)

  function finishMotion() {
    if (!reducedMotion && !systemReducedMotion) return
    for (const animation of menu?.getAnimations() ?? []) {
      if (animation.playState === "finished") continue
      // Svelte may replace its initial delay animation with the surface animation.
      animation.addEventListener("finish", finishMotion, { once: true })
      animation.finish()
    }
  }

  function menuSurface(node: HTMLElement) {
    return ({ direction = "in" }: { direction?: "in" | "out" } = {}) => surface(node, {
      duration: reducedMotion || systemReducedMotion ? 0 : direction === "in" ? 160 : 120,
    })
  }

  onMount(() => {
    const media = window.matchMedia("(prefers-reduced-motion: reduce)")
    function dropdownMotionChanged() {
      systemReducedMotion = media.matches
      if (media.matches) finishMotion()
    }
    dropdownMotionChanged()
    media.addEventListener("change", dropdownMotionChanged)
    return () => media.removeEventListener("change", dropdownMotionChanged)
  })

  $effect(() => {
    if (reducedMotion || systemReducedMotion) {
      // Also finish an exit already in progress when the preference changes.
      finishMotion()
    }
  })

  $effect(() => {
    if (disabled && open) onOpenChange(false)
  })

  function exitStarted() {
    if (!menu) return
    menu.setAttribute("aria-hidden", "true")
    if (menu.contains(document.activeElement)) {
      if (!disabled && button?.isConnected && !button.closest("dialog:not([open])")) button.focus({ preventScroll: true })
      else if (document.activeElement instanceof HTMLElement) document.activeElement.blur()
    }
  }

  function dismiss(event: Event) {
    if (open && event.target instanceof Node && !control?.contains(event.target) && !menu?.contains(event.target)) onOpenChange(false)
  }
  function keydown(event: KeyboardEvent) {
    if (event.key === "Escape" && open && !disabled && !menu?.inert) {
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
    if (!open || disabled || menu?.inert) return
    const items = Array.from(menu?.querySelectorAll<HTMLButtonElement>("[data-option]") ?? [])
    if (!items.length) return
    const index = items.indexOf(document.activeElement as HTMLButtonElement)
    const next = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 :
      index < 0 ? (event.key === "ArrowUp" ? items.length - 1 : Math.max(0, options.findIndex(option => option.value === selected))) :
      (index + (event.key === "ArrowUp" ? -1 : 1) + items.length) % items.length
    items[next]?.focus()
  }
  function select(value: Value) {
    if (!open || disabled || menu?.inert) return
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
    <div {id} bind:this={menu} use:floatingDropdown={{ anchor: button, onClose: () => onOpenChange(false) }}
      class={ui.menu + " dropdown-menu overflow-y-auto overscroll-contain"} role="group" aria-label={label}
      transition:menuSurface|global
      onoutrostart={exitStarted} onintrostart={() => menu?.removeAttribute("aria-hidden")}>
      {#each options as option}
        <button type="button" data-option class={ui.menuItem + (selected === option.value ? " " + ui.menuItemActive : "")} onclick={() => select(option.value)} onkeydown={navigate} aria-pressed={selected === option.value}>
          {#if optionIcon}{@render optionIcon(option.value)}{/if}
          <span lang={option.lang}>{option.label}</span>
          <span class="grid size-[14px] !flex-none place-items-center">
            {#if selected === option.value}<span class="grid" in:surface={{ y: 0, duration: reducedMotion || systemReducedMotion ? 0 : 160 }}><Check size={14} /></span>{/if}
          </span>
        </button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .dropdown-menu:global([inert]) { pointer-events: none; }
  .dropdown-menu {
    position: fixed;
    z-index: 60;
    inset: auto;
    margin: 0;
    box-sizing: border-box;
    max-width: none;
    color: var(--text);
  }
</style>
