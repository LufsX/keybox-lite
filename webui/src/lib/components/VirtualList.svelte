<script lang="ts" generics="T">
  import { onMount, tick, untrack, type Snippet } from "svelte"
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

  const motionDuration = 180
  let active = false
  let motionUntil = 0
  let anchorSuppressedUntil = 0
  let changingHeight = $state(false)
  let motionTimer: ReturnType<typeof setTimeout> | undefined
  let entering = new Set<string>()
  let leaving = new Set<string>()
  let batchDuration = motionDuration
  const motions = new Map<HTMLElement, Animation>()
  type MotionBatch = { tops: Map<string, number>; entering: Set<string>; leaving: Set<string>; duration: number }
  let pendingMotion: MotionBatch | undefined
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

  function snapshot(added = new Set<string>(), duration = motionDuration): MotionBatch {
    const tops = new Map<string, number>()
    for (const [node, key] of nodes) tops.set(key, node.getBoundingClientRect().top)
    return { tops, entering: added, leaving: new Set(leaving), duration }
  }

  function stopMotion() {
    for (const animation of motions.values()) animation.cancel()
    motions.clear()
    changingHeight = false
    pendingMotion = undefined
    motionUntil = 0
    clearTimeout(motionTimer)
    motionTimer = undefined
  }

  function playMotion(batch: MotionBatch) {
    if (!root) return
    const top = root.getBoundingClientRect().top
    // Read all positions before starting animations, including interrupted moves.
    const moves = [...nodes].map(([node, key]) => {
      const row = current?.positions.get(key)
      const offset = row === undefined || batch.leaving.has(key) ? undefined : current!.offset(row)
      const before = batch.tops.get(key)
      return { node, key, offset, from: before === undefined ? undefined : before - top }
    })
    for (const { node, key, offset, from } of moves) {
      motions.get(node)?.cancel()
      motions.delete(node)
      const added = from === undefined && batch.entering.has(key)
      if (from === undefined && !added) continue
      // An outgoing parent keeps its screen position while its content fades.
      const to = offset ?? from!
      const initial = from ?? to + 6
      if (offset !== undefined && Math.abs(initial - to) < 0.5) continue
      const animation = node.animate([
        { transform: `translateY(${initial}px)` },
        { transform: `translateY(${offset === undefined ? initial : to}px)` },
      ], { duration: batch.duration, easing: "cubic-bezier(.22, 1, .36, 1)", fill: "both" })
      motions.set(node, animation)
      animation.onfinish = () => {
        // Outgoing rows retain their frozen position until Svelte removes them.
        if (current?.positions.has(key) && motions.get(node) === animation) {
          animation.cancel()
          motions.delete(node)
        }
      }
    }
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
      moveScroll(Math.max(0, desired) - localTop)
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
      if (changed && current) {
        rememberAnchor()
        if (anchor && !next.positions.has(anchor.key)) {
          // Filtering the anchor retains a nearby survivor instead of jumping to
          // the start. Its old position may be below the top of the viewport.
          const oldRow = current.positions.get(anchor.key)!
          let survivor = oldRow
          while (survivor < current.keys.length && !next.positions.has(current.keys[survivor])) survivor++
          if (survivor === current.keys.length) {
            survivor = oldRow - 1
            while (survivor >= 0 && !next.positions.has(current.keys[survivor])) survivor--
          }
          anchor = survivor < 0 ? null : {
            key: current.keys[survivor], within: localTop - current.offset(survivor),
          }
        }
      }
      if (changed) {
        entering = new Set(next.keys.filter(key => !current?.positions.has(key)))
        leaving = new Set()
        batchDuration = reordered ? 260 : motionDuration
        if (reordered && root) {
          const view = readViewport()
          const range = virtualRange(next, view.top - root.getBoundingClientRect().top, view.height, overscan)
          const focused = focusedKey === null ? undefined : next.positions.get(focusedKey)
          const destination = new Set(virtualRows(range.start, range.end, focused).map(row => next.keys[row]))
          const mounted = new Set(nodes.values())
          // Sorting can replace the entire viewport even when the catalog is
          // unchanged. Fade those rows as well as animating shared rows.
          for (const key of destination) if (!mounted.has(key)) entering.add(key)
          for (const key of mounted) if (!destination.has(key)) leaving.add(key)
        }
        const reduced = typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches
        pendingMotion = active && !reduced ? snapshot(entering, batchDuration) : undefined
        // Inside a scrolled list, expose the destination height immediately so
        // scroll bounds allow anchor compensation before the next paint.
        changingHeight = active && !reduced && !anchor && !reordered
        motionUntil = active && !reduced ? performance.now() + batchDuration : 0
        clearTimeout(motionTimer)
        motionTimer = setTimeout(stopMotion, batchDuration + 60)
      }
      if (reordered) {
        // A selection reorder moves rows, not the user's viewport.
        anchor = null
        anchorSuppressedUntil = performance.now() + batchDuration + 60
      } else if (!changed) {
        rememberAnchor()
      }
      current = next
      if (active && root) {
        // Render the destination slice in this flush, before applying the anchor.
        const view = readViewport()
        const row = anchor ? next.positions.get(anchor.key) : undefined
        const top = row === undefined ? view.top - root.getBoundingClientRect().top : Math.max(0, next.offset(row) + anchor!.within)
        const range = virtualRange(next, top, view.height, overscan)
        start = range.start
        end = range.end
      }
      // Bound the keyed height cache when searches/catalog refreshes remove items.
      for (const key of sizes.keys()) if (!next.positions.has(key)) sizes.delete(key)
      schedule()
    })
  })

  $effect(() => {
    const element = root
    const explicit = scrollTarget
    const useWindow = windowScroll
    void overscan
    untrack(() => {
      target = explicit ?? (!useWindow ? element?.closest("dialog") ?? null : null)
      anchor = null
      schedule()
    })
  })

  $effect(() => {
    const next = index
    void revision
    void rows
    untrack(() => {
      if (pendingMotion) {
        const batch = pendingMotion
        pendingMotion = undefined
        if (frame) cancelAnimationFrame(frame)
        update()
        playMotion(batch)
      }
      // A later viewport slice must not inherit a catalog animation time window.
      void tick().then(() => { if (current === next) entering.clear() })
    })
  })

  function rowFade(_node: HTMLElement, { key }: { key: string }) {
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches
    const animate = entering.has(key) || leaving.has(key) || !index.positions.has(key)
    return { duration: reduced || !animate ? 0 : batchDuration, css: (t: number) => "opacity:" + t }
  }

  function measure(node: HTMLElement, key: string) {
    nodes.set(node, key)
    observer?.observe(node)
    return {
      update(nextKey: string) { nodes.set(node, nextKey) },
      destroy() {
        observer?.unobserve(node)
        nodes.delete(node)
        motions.get(node)?.cancel()
        motions.delete(node)
      },
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
        if (changed) {
          if (!pendingMotion && motions.size && performance.now() < motionUntil) {
            pendingMotion = snapshot(new Set(), Math.max(0, motionUntil - performance.now()))
            // Measurement corrections only retarget rows already moving.
            for (const [node, key] of nodes) if (!motions.has(node)) pendingMotion.tops.delete(key)
          }
          revision++
          schedule()
        } else if (!hadAnchor) anchor = null
      })
      if (root) observer.observe(root)
      if (target) observer.observe(target)
      for (const node of nodes.keys()) observer.observe(node)
    }
    const media = window.matchMedia("(prefers-reduced-motion: reduce)")
    const reduceMotion = () => {
      if (!media.matches) return
      stopMotion()
      for (const node of nodes.keys()) {
        for (const animation of node.firstElementChild?.getAnimations() ?? []) animation.finish()
      }
    }
    media.addEventListener("change", reduceMotion)
    // Capture receives scroll events from native dialog descendants as well.
    window.addEventListener("scroll", schedule, { passive: true, capture: true })
    window.addEventListener("resize", schedule, { passive: true })
    schedule()
    return () => {
      active = false
      stopMotion()
      media.removeEventListener("change", reduceMotion)
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

<div bind:this={root} class="virtual-list" class:is-changing-height={changingHeight} style:height={total + "px"}
  onfocusin={focus} onfocusout={blur}>
  {#each rows as row (row.key)}
    <div use:measure={row.key} data-virtual-key={row.key} data-index={row.index}
      class="virtual-row" style:transform={"translateY(" + row.offset + "px)"}>
      <div class="virtual-content" transition:rowFade|global={{ key: row.key }}>
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
  .is-changing-height {
    transition: height 180ms cubic-bezier(.2, .7, .2, 1);
    overflow: clip;
  }
  @media (prefers-reduced-motion: reduce) {
    .virtual-list { transition: none; }
  }
</style>
