<script lang="ts" generics="T">
  import { onMount, untrack, type Snippet } from "svelte"
  import { cubicOut } from "svelte/easing"
  import { VirtualIndex, virtualRange, virtualRows } from "../virtual"

  let {
    items, getKey, item, estimateSize = 72, overscan = 6,
    windowScroll = true, scrollTarget,
  }: {
    items: readonly T[]
    getKey: (item: T) => string
    item: Snippet<[T, number]>
    estimateSize?: number
    overscan?: number
    windowScroll?: boolean
    /** Overrides windowScroll; otherwise false uses the closest dialog. */
    scrollTarget?: HTMLElement | null
  } = $props()

  const sizes = new Map<string, number>()
  let index = $derived(new VirtualIndex(items.map(getKey), sizes, estimateSize))
  let root = $state<HTMLDivElement>()
  let revision = $state(0)
  let start = $state(0)
  let end = $state(20)
  let focusedKey = $state<string | null>(null)
  let rows = $derived.by(() => {
    // The index is mutable so measurements explicitly invalidate offsets and total.
    void revision
    const focused = focusedKey === null ? undefined : index.positions.get(focusedKey)
    return virtualRows(Math.min(start, Math.max(0, items.length - 1)), Math.min(Math.max(end, start + 1), items.length), focused)
      .map(i => ({ item: items[i], index: i, key: index.keys[i], offset: index.offset(i) }))
  })
  let total = $derived.by(() => { void revision; return index.total })

  let active = false
  let catalogMotionUntil = 0
  let anchorSuppressedUntil = 0
  let reordering = $state(false)
  let reorderTimer: ReturnType<typeof setTimeout> | undefined
  let frame = 0
  let target: HTMLElement | null = null
  let current: VirtualIndex | undefined
  let localTop = 0
  let viewport = 0
  let anchor: { key: string; within: number } | null = null
  let observer: ResizeObserver | undefined
  const nodes = new Map<HTMLElement, string>()

  function readViewport() {
    const inset = parseFloat(getComputedStyle(target ?? document.documentElement).scrollPaddingTop) || 0
    const top = target ? target.getBoundingClientRect().top + target.clientTop : 0
    return { top: top + inset, height: Math.max(0, (target ? target.clientHeight : window.innerHeight) - inset) }
  }

  function rememberAnchor() {
    if (performance.now() < anchorSuppressedUntil || anchor || !current?.keys.length || !root) return
    const view = readViewport()
    localTop = view.top - root.getBoundingClientRect().top
    viewport = view.height
    if (localTop < 0 || !viewport) return
    const row = current.at(localTop)
    anchor = { key: current.keys[row], within: localTop - current.offset(row) }
  }

  function schedule(event?: Event) {
    if (event?.type === "scroll" && (target ? event.target !== target : event.target !== document && event.target !== window)) return
    if (active && !frame) frame = requestAnimationFrame(update)
  }

  function moveScroll(delta: number) {
    if (Math.abs(delta) < 0.5) return
    if (target) target.scrollTop += delta
    else window.scrollBy({ top: delta, behavior: "instant" })
  }

  function update() {
    frame = 0
    if (!root || !current) return
    const bounds = root.getBoundingClientRect()
    const view = readViewport(), top = view.top
    viewport = view.height
    localTop = top - bounds.top
    if (anchor) {
      const row = current.positions.get(anchor.key)
      const desired = row === undefined ? 0 : current.offset(row) + anchor.within
      // The scroll container also contains headers and trailing actions. Let its
      // native bounds clamp the offset; clamping to list height snaps at the end.
      const next = Math.max(0, desired)
      moveScroll(next - localTop)
      anchor = null
      localTop = top - root.getBoundingClientRect().top
    }
    const range = virtualRange(current, localTop, viewport, overscan)
    start = range.start
    end = range.end
  }

  $effect.pre(() => {
    const next = index
    untrack(() => {
      const changed = !current || current.keys.length !== next.keys.length || current.keys.some((key, i) => key !== next.keys[i])
      const reordered = changed && current && current.keys.length === next.keys.length && next.keys.every(key => current!.positions.has(key))
      if (reordered) {
        // A selection reorder moves rows, not the user's viewport. Ignore the
        // following measurement batch's anchor compensation as well.
        anchor = null
        anchorSuppressedUntil = performance.now() + 240
        reordering = true
        clearTimeout(reorderTimer)
        reorderTimer = setTimeout(() => { reordering = false; reorderTimer = undefined }, 240)
      } else {
        rememberAnchor()
        if (changed) {
          catalogMotionUntil = performance.now() + 240
          reordering = false
          clearTimeout(reorderTimer)
          reorderTimer = undefined
        }
      }
      current = next
      // Bound the keyed height cache when searches/catalog refreshes remove items.
      for (const key of sizes.keys()) if (!next.positions.has(key)) sizes.delete(key)
      schedule()
    })
  })

  $effect(() => {
    const element = root
    const explicit = scrollTarget
    const useWindow = windowScroll
    // Read overscan here so changing it also schedules a range update.
    void overscan
    untrack(() => {
      target = explicit ?? (!useWindow ? element?.closest("dialog") ?? null : null)
      anchor = null
      schedule()
    })
  })

  function rowMotion(node: HTMLElement, { key, order }: { key: string; order: number }, entering: boolean) {
    const style = entering ? undefined : getComputedStyle(node)
    const opacity = style ? parseFloat(style.opacity) : 1
    const translate = style && style.transform !== "none" ? new DOMMatrixReadOnly(style.transform).m42 : 0
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches
    // Recycling an offscreen row is a scroll operation, not a catalog change.
    const animate = entering ? !active || current !== index || performance.now() < catalogMotionUntil : !index.positions.has(key)
    return {
      duration: reduced || !animate ? 0 : entering ? 180 : 140,
      delay: reduced || !animate || !entering ? 0 : Math.min(Math.max(0, order - start), 7) * 12,
      easing: cubicOut,
      css: (t: number) => "opacity:" + t * opacity + ";transform:translateY(" + (entering ? (1 - t) * 6 : -4 + (translate + 4) * t) + "px)",
    }
  }

  function rowEnter(node: HTMLElement, options: { key: string; order: number }) { return rowMotion(node, options, true) }
  function rowExit(node: HTMLElement, options: { key: string; order: number }) { return rowMotion(node, options, false) }

  function measure(node: HTMLElement, key: string) {
    nodes.set(node, key)
    observer?.observe(node)
    return {
      update(nextKey: string) { nodes.set(node, nextKey) },
      destroy() { observer?.unobserve(node); nodes.delete(node) },
    }
  }

  function focus(event: FocusEvent) {
    const row = event.target instanceof Element ? event.target.closest<HTMLElement>("[data-virtual-key]") : null
    focusedKey = row && root?.contains(row) ? row.dataset.virtualKey ?? null : null
  }

  function blur(event: FocusEvent) {
    if (!(event.relatedTarget instanceof Node) || !root?.contains(event.relatedTarget)) focusedKey = null
  }

  onMount(() => {
    active = true
    if (typeof ResizeObserver !== "undefined") {
      observer = new ResizeObserver(entries => {
        if (!current) return
        let changed = false
        const hadAnchor = anchor !== null
        for (const entry of entries) {
          const node = entry.target as HTMLElement
          if (node === root || node === target) { schedule(); continue }
          const key = nodes.get(node)
          const row = key === undefined ? undefined : current.positions.get(key)
          if (row === undefined || key === undefined) continue
          const height = entry.borderBoxSize?.[0]?.blockSize ?? node.getBoundingClientRect().height
          rememberAnchor()
          if (current.measure(row, height)) {
            sizes.set(key, height)
            changed = true
          }
        }
        if (changed) { revision++; schedule() }
        else if (!hadAnchor) anchor = null
      })
      if (root) observer.observe(root)
      if (target) observer.observe(target)
      for (const node of nodes.keys()) observer.observe(node)
    }
    // Capture receives scroll events from native dialog descendants as well.
    window.addEventListener("scroll", schedule, { passive: true, capture: true })
    window.addEventListener("resize", schedule, { passive: true })
    schedule()
    return () => {
      active = false
      clearTimeout(reorderTimer)
      observer?.disconnect()
      observer = undefined
      window.removeEventListener("scroll", schedule, true)
      window.removeEventListener("resize", schedule)
      if (frame) cancelAnimationFrame(frame)
      frame = 0
      nodes.clear()
    }
  })

  $effect(() => {
    const element = scrollTarget ?? (!windowScroll ? root?.closest("dialog") : null)
    if (element) observer?.observe(element)
    return () => { if (element) observer?.unobserve(element) }
  })
</script>

<div bind:this={root} class="virtual-list" class:is-reordering={reordering} style:height={total + "px"}
  onfocusin={focus} onfocusout={blur}>
  {#each rows as row (row.key)}
    <div use:measure={row.key} data-virtual-key={row.key} data-index={row.index}
      class="virtual-row" style:transform={"translateY(" + row.offset + "px)"}>
      <div class="virtual-content" in:rowEnter|global={{ key: row.key, order: row.index }} out:rowExit|global={{ key: row.key, order: row.index }}>
        {@render item(row.item, row.index)}
      </div>
    </div>
  {/each}
</div>

<style>
  .virtual-list {
    position: relative;
    width: 100%;
    flex-shrink: 0;
    overflow-anchor: none;
  }
  .virtual-row {
    position: absolute;
    top: 0;
    left: 0;
    width: 100%;
    display: flow-root;
  }
  .is-reordering .virtual-row { transition: transform 180ms cubic-bezier(.2, .7, .2, 1); }
  @media (prefers-reduced-motion: reduce) {
    .virtual-row { transition: none; }
  }
</style>
