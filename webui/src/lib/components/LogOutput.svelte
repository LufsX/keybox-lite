<script lang="ts">
  import { ArrowDown, Terminal } from "lucide-svelte"
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
    bottomLabel?: string
    saved: boolean
    loading: boolean
    disabled?: boolean
    onCurrent: () => void
    onSaved: () => void
  }

  let { output, lines, title, hint, currentLabel, savedLabel, loadingLabel,
    bottomLabel = "Back to bottom", saved, loading, disabled = false, onCurrent, onSaved }: Props = $props()
  const id = $props.id()
  let contentHeight = $state(34)
  let currentBody: HTMLElement | undefined
  const scrollPositions = [0, 0]
  let followingCurrent = $state(true)
  let currentOverflow = $state(false)
  let resumeCurrent: (() => void) | undefined
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
    const index = view ? 1 : 0
    let frame = 0
    let ready = false
    let destroyed = false
    let appliedTop = 0
    let appliedWidth = 0
    let appliedHeight = 0
    const active = () => currentBody === node && view === saved && !node.inert && node.isConnected
    const bottom = () => Math.max(0, node.scrollHeight - node.clientHeight)
    const scroll = () => {
      if (!ready || node.clientWidth !== appliedWidth || node.clientHeight !== appliedHeight) return
      const top = Math.max(0, node.scrollTop)
      // Ignore our own writes and browser clamping after a layout/content change.
      if (Math.abs(top - Math.min(appliedTop, bottom())) <= 0.5) return
      appliedTop = top
      scrollPositions[index] = top
      if (!view) {
        followingCurrent = bottom() - top <= 2
        currentOverflow = bottom() > 2
      }
    }
    const layout = () => {
      if (!active()) return
      scroll()
      contentHeight = node.getBoundingClientRect().height
      node.scrollTop = !view && followingCurrent ? bottom() : scrollPositions[index]
      appliedTop = node.scrollTop
      appliedWidth = node.clientWidth
      appliedHeight = node.clientHeight
      scrollPositions[index] = appliedTop
      if (!view) currentOverflow = bottom() > 2
      ready = true
    }
    const schedule = () => {
      if (!frame && !destroyed) frame = requestAnimationFrame(() => { frame = 0; layout() })
    }
    const refresh = () => {
      queueMicrotask(() => { if (!destroyed) { layout(); schedule() } })
    }
    const resume = () => {
      if (!active()) return
      scroll()
      followingCurrent = true
      layout()
    }
    const activate = () => {
      if (view !== saved) return
      currentBody = node
      node.id = id
      node.removeAttribute("aria-hidden")
      node.inert = false
      if (!view) resumeCurrent = resume
      refresh()
    }
    const deactivate = () => {
      scroll()
      ready = false
      cancelAnimationFrame(frame)
      frame = 0
    }
    const update = (next: { view: boolean; content: string }) => {
      if (view !== saved) return
      if (!view && !next.content) {
        followingCurrent = true
        scrollPositions[0] = 0
      }
      refresh()
    }
    node.addEventListener("introstart", activate)
    node.addEventListener("outrostart", deactivate)
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(schedule)
    observer?.observe(node)
    if (!observer) window.addEventListener("resize", schedule)
    const onScroll = () => { if (active()) scroll() }
    node.addEventListener("scroll", onScroll, { passive: true })
    activate()
    update(options)
    return { update, destroy() {
      destroyed = true
      cancelAnimationFrame(frame)
      observer?.disconnect()
      if (!observer) window.removeEventListener("resize", schedule)
      node.removeEventListener("scroll", onScroll)
      node.removeEventListener("introstart", activate)
      node.removeEventListener("outrostart", deactivate)
      if (currentBody === node) currentBody = undefined
      if (resumeCurrent === resume) resumeCurrent = undefined
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
    <BusyIndicator active={saved && loading} label={loadingLabel} />
  </div>
  <div class="mb-2 flex items-center gap-1">
    <div class="log-views" use:followSelection={saved} role="group" aria-label={title}>
      <span class="log-view-indicator" class:is-ready={indicatorReady} aria-hidden="true"
        style:width={indicator.width + "px"} style:height={indicator.height + "px"}
        style:transform={"translate(" + indicator.x + "px, " + indicator.y + "px)"}></span>
      <button type="button" aria-pressed={!saved} aria-controls={id} onclick={onCurrent}>{currentLabel}</button>
      <button type="button" aria-pressed={saved} aria-controls={id} class:is-disabled={disabled} {disabled} aria-busy={loading}
        onclick={onSaved}>{savedLabel}</button>
    </div>
  </div>
  <div class="log-content" style:height={contentHeight + "px"}>
    {#key saved}
      <pre {id} use:measureBody={{ view: saved, content: output }} aria-label={saved ? savedLabel : currentLabel} aria-busy={saved && loading}
        class="log-body m-0 max-h-[300px] w-full overflow-y-auto whitespace-pre-wrap p-2 font-mono text-[11px] leading-normal text-[var(--text)] [overflow-wrap:anywhere]"
        in:surface={{ y: 3, scale: 1, duration: 200 }} out:leave>{#if output}{#each lines as line (line)}{#each line.parts as part}<span class={part.tone ? toneClasses[part.tone] : undefined}>{part.text}</span>{/each}{/each}{:else}{hint}{/if}</pre>
    {/key}
    {#if !saved && !followingCurrent && currentOverflow}
      <button type="button" class="log-bottom glass-control" aria-controls={id} onclick={() => resumeCurrent?.()}>
        <ArrowDown size={12} class="shrink-0" aria-hidden="true" />{bottomLabel}
      </button>
    {/if}
  </div>
</section>

<style>
  .log-views { position: relative; isolation: isolate; display: flex; flex-wrap: wrap; gap: 2px; min-width: 0; padding: 2px; border-radius: 6px; background: var(--glass-inset, var(--inset)); box-shadow: var(--glass-inset-shadow, none); }
  .log-view-indicator { position: absolute; z-index: -1; top: 0; left: 0; border-radius: 4px; background: var(--glass-control, var(--panel)); box-shadow: inset 0 1px 0 var(--glass-highlight, transparent), 0 1px 3px #0000001a; opacity: 0; pointer-events: none; }
  .log-view-indicator.is-ready { opacity: 1; transition: transform 260ms cubic-bezier(.22, 1, .36, 1), width 260ms cubic-bezier(.22, 1, .36, 1), height 260ms cubic-bezier(.22, 1, .36, 1), opacity 150ms; }
  .log-views button { min-height: 28px; min-width: 0; padding: 3px 8px; border-radius: 4px; font-size: 11px; font-weight: 550; color: var(--muted); overflow-wrap: anywhere; transition: color 150ms; }
  .log-views button[aria-pressed="true"] { color: var(--accent); }
  .log-views button:disabled { cursor: default; }
  .log-views button.is-disabled { opacity: .5; }
  .log-content { position: relative; overflow: hidden; border-radius: 8px; background: var(--glass-inset, var(--inset)); box-shadow: var(--glass-inset-shadow, none); transition: height 260ms cubic-bezier(.22, 1, .36, 1); }
  .log-body { position: absolute; inset: 0 0 auto; overflow-anchor: none; }
  .log-bottom { position: absolute; z-index: 1; inset-inline-end: 6px; bottom: 6px; display: inline-flex; align-items: center; gap: 4px; max-width: calc(100% - 12px); padding: 3px 6px; border: 1px solid var(--line); border-radius: 4px; background: var(--glass-control, var(--panel)); box-shadow: var(--glass-inset-shadow, none); font-size: 11px; line-height: 1.5; color: var(--accent); overflow-wrap: anywhere; }
  @media (prefers-reduced-motion: reduce) { .log-content, .log-views button, .log-view-indicator.is-ready { transition: none; } }
</style>
