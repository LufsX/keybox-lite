<script lang="ts">
  import { Terminal } from "lucide-svelte"
  import { fade } from "svelte/transition"
  import { surface } from "../motion"
  import type { LogLine, LogTone } from "../log"
  import { ui } from "../ui"
  import BusyIndicator from "./BusyIndicator.svelte"

  interface Props {
    output: string
    lines: LogLine[]
    title: string
    hint: string
    currentLabel: string
    savedLabel: string
    loadingLabel: string
    saved: boolean
    loading: boolean
    disabled?: boolean
    onCurrent: () => void
    onSaved: () => void
  }

  let { output, lines, title, hint, currentLabel, savedLabel, loadingLabel,
    saved, loading, disabled = false, onCurrent, onSaved }: Props = $props()
  const id = $props.id()
  let contentHeight = $state(34)
  let currentBody: HTMLElement | undefined
  const scrollPositions = [0, 0]
  let indicatorReady = $state(false)
  let indicator = $state({ x: 2, y: 2, width: 0, height: 28 })

  function followSelection(node: HTMLElement, view: boolean) {
    let active = view
    let frame = 0
    const measure = () => {
      frame = 0
      const button = node.querySelectorAll("button")[active ? 1 : 0]
      if (!button) return
      indicator = { x: button.offsetLeft, y: button.offsetTop, width: button.offsetWidth, height: button.offsetHeight }
      indicatorReady = true
    }
    const schedule = () => { if (!frame) frame = requestAnimationFrame(measure) }
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(schedule)
    observer?.observe(node)
    for (const button of node.querySelectorAll("button")) observer?.observe(button)
    window.addEventListener("resize", schedule)
    schedule()
    return {
      update(next: boolean) { active = next; schedule() },
      destroy() { cancelAnimationFrame(frame); observer?.disconnect(); window.removeEventListener("resize", schedule) },
    }
  }

  function measureBody(node: HTMLElement, options: { view: boolean; content: string }) {
    const view = options.view
    currentBody = node
    const resize = () => {
      if (currentBody === node && node.isConnected) contentHeight = node.getBoundingClientRect().height
    }
    const scroll = () => { scrollPositions[view ? 1 : 0] = node.scrollTop }
    const activate = () => {
      currentBody = node
      node.id = id
      node.removeAttribute("aria-hidden")
      node.inert = false
      resize()
      node.scrollTop = scrollPositions[view ? 1 : 0]
    }
    node.addEventListener("introstart", activate)
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(resize)
    observer?.observe(node)
    if (!observer) window.addEventListener("resize", resize)
    node.addEventListener("scroll", scroll, { passive: true })
    queueMicrotask(() => { if (currentBody === node) { resize(); node.scrollTop = scrollPositions[view ? 1 : 0] } })
    return { update() { queueMicrotask(resize) }, destroy() {
      observer?.disconnect()
      if (!observer) window.removeEventListener("resize", resize)
      node.removeEventListener("scroll", scroll)
      node.removeEventListener("introstart", activate)
      if (currentBody === node) currentBody = undefined
    } }
  }
  function leave(node: HTMLElement) {
    node.removeAttribute("id")
    node.setAttribute("aria-hidden", "true")
    node.inert = true
    return { ...fade(node, { duration: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : 140 }) }
  }

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
    <BusyIndicator active={loading} label={loadingLabel} />
  </div>
  <div class="mb-2 flex items-center gap-1">
    <div class="log-views" use:followSelection={saved} role="group" aria-label={title}>
      <span class="log-view-indicator" class:is-ready={indicatorReady} aria-hidden="true"
        style:width={indicator.width + "px"} style:height={indicator.height + "px"}
        style:transform={"translate(" + indicator.x + "px, " + indicator.y + "px)"}></span>
      <button type="button" aria-pressed={!saved} aria-controls={id} onclick={onCurrent}>{currentLabel}</button>
      <button type="button" aria-pressed={saved} aria-controls={id} class:is-disabled={disabled} disabled={disabled || loading}
        onclick={onSaved}>{savedLabel}</button>
    </div>
  </div>
  <div class="log-content" style:height={contentHeight + "px"}>
    {#key saved}
      <pre {id} use:measureBody={{ view: saved, content: output }} aria-label={saved ? savedLabel : currentLabel} aria-busy={loading}
        class="log-body m-0 max-h-[300px] w-full overflow-y-auto whitespace-pre-wrap p-2 font-mono text-[11px] leading-normal text-[var(--text)] [overflow-wrap:anywhere]"
        in:surface={{ y: 3, scale: 1, duration: 200 }} out:leave>{#if output}{#each lines as line (line)}{#each line.parts as part}<span class={part.tone ? toneClasses[part.tone] : undefined}>{part.text}</span>{/each}{/each}{:else}{hint}{/if}</pre>
    {/key}
  </div>
</section>

<style>
  .log-views { position: relative; isolation: isolate; display: flex; flex-wrap: wrap; gap: 2px; min-width: 0; padding: 2px; border-radius: 6px; background: var(--inset); }
  .log-view-indicator { position: absolute; z-index: -1; top: 0; left: 0; border-radius: 4px; background: var(--panel); box-shadow: 0 1px 3px #0000001a; opacity: 0; pointer-events: none; }
  .log-view-indicator.is-ready { opacity: 1; transition: transform 260ms cubic-bezier(.22, 1, .36, 1), width 260ms cubic-bezier(.22, 1, .36, 1), height 260ms cubic-bezier(.22, 1, .36, 1), opacity 150ms; }
  .log-views button { min-height: 28px; min-width: 0; padding: 3px 8px; border-radius: 4px; font-size: 11px; font-weight: 550; color: var(--muted); overflow-wrap: anywhere; transition: color 150ms; }
  .log-views button[aria-pressed="true"] { color: var(--accent); }
  .log-views button:disabled { cursor: default; }
  .log-views button.is-disabled { opacity: .5; }
  .log-content { position: relative; overflow: hidden; border-radius: 8px; background: var(--inset); transition: height 260ms cubic-bezier(.22, 1, .36, 1); }
  .log-body { position: absolute; inset: 0 0 auto; }
  @media (prefers-reduced-motion: reduce) { .log-content, .log-views button, .log-view-indicator.is-ready { transition: none; } }
</style>
