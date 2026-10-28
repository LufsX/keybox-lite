export interface Bounds { left: number; top: number; right: number; bottom: number; width: number }
export interface Viewport { left: number; top: number; width: number; height: number }

export function dropdownPosition(anchor: Bounds, menu: { width: number; height: number }, viewport: Viewport) {
  const gap = 4, edge = 8
  const leftEdge = viewport.left + edge, topEdge = viewport.top + edge
  const rightEdge = viewport.left + viewport.width - edge
  const bottomEdge = viewport.top + viewport.height - edge
  const width = Math.min(Math.max(anchor.width, menu.width), Math.max(0, rightEdge - leftEdge))
  const below = Math.max(0, bottomEdge - anchor.bottom - gap)
  const above = Math.max(0, anchor.top - gap - topEdge)
  const flipped = menu.height > below && above > below
  const maxHeight = Math.min(320, flipped ? above : below)
  const height = Math.min(menu.height, maxHeight)
  return {
    left: Math.max(leftEdge, Math.min(anchor.right - width, rightEdge - width)),
    top: Math.max(topEdge, Math.min(flipped ? anchor.top - gap - height : anchor.bottom + gap, bottomEdge - height)),
    width, maxHeight, flipped,
  }
}

// Keep the fallback inside its modal so showModal() does not make it inert.
export function floatingDropdown(node: HTMLElement, anchor: HTMLButtonElement) {
  const dialog = anchor.closest("dialog")
  const viewport = window.visualViewport
  let disposed = false
  let frame = 0
  let topLayer = false
  let moved = false
  let previousOverflow: string | undefined
  let trackingUntil = 0
  let observer: ResizeObserver | undefined
  node.style.visibility = "hidden"

  function position() {
    if (disposed) return
    const bounds = anchor.getBoundingClientRect()
    const view = {
      left: viewport?.offsetLeft ?? 0, top: viewport?.offsetTop ?? 0,
      width: viewport?.width ?? window.innerWidth, height: viewport?.height ?? window.innerHeight,
    }
    // An animated dialog establishes a containing block for fixed descendants.
    const relative = !topLayer && dialog && getComputedStyle(dialog).transform !== "none"
    const origin = relative ? dialog.getBoundingClientRect() : undefined
    const scaleX = origin && dialog ? origin.width / dialog.offsetWidth : 1
    const scaleY = origin && dialog ? origin.height / dialog.offsetHeight : 1
    // Measure at the intended width, independently of the previous viewport size.
    const width = dropdownPosition(bounds, { width: 120, height: 0 }, view).width
    node.style.width = `${width / scaleX}px`
    node.style.maxHeight = "none"
    const placement = dropdownPosition(bounds, {
      width: 120, height: (node.scrollHeight + node.offsetHeight - node.clientHeight) * scaleY,
    }, view)
    node.style.left = `${(placement.left - (origin?.left ?? 0)) / scaleX - (relative ? dialog.clientLeft : 0)}px`
    node.style.top = `${(placement.top - (origin?.top ?? 0)) / scaleY - (relative ? dialog.clientTop : 0)}px`
    node.style.width = `${placement.width / scaleX}px`
    node.style.maxHeight = `${placement.maxHeight / scaleY}px`
    node.style.transformOrigin = placement.flipped ? "bottom right" : "top right"
    node.style.visibility = "visible"
  }

  function update() {
    frame = 0
    position()
    if (performance.now() < trackingUntil) schedule()
  }
  function schedule() {
    if (!disposed && !frame) frame = requestAnimationFrame(update)
  }
  function animationFinished(event: AnimationEvent) {
    if (event.target !== dialog) return
    trackingUntil = 0
    schedule()
  }

  // Actions run before Svelte inserts the node. Popovers require a connected node.
  frame = requestAnimationFrame(() => {
    frame = 0
    if (disposed) return
    if (typeof node.showPopover === "function") {
      node.setAttribute("popover", "manual")
      try { node.showPopover(); topLayer = true } catch { node.removeAttribute("popover") }
    }
    if (!topLayer && dialog) {
      previousOverflow = dialog.style.overflow
      dialog.style.overflow = "visible"
      dialog.appendChild(node)
      moved = true
    }
    window.addEventListener("scroll", schedule, true)
    window.addEventListener("resize", schedule)
    viewport?.addEventListener("scroll", schedule)
    viewport?.addEventListener("resize", schedule)
    dialog?.addEventListener("animationend", animationFinished)
    dialog?.addEventListener("animationcancel", animationFinished)
    if (typeof ResizeObserver !== "undefined") {
      observer = new ResizeObserver(schedule)
      observer.observe(anchor)
      observer.observe(node)
      if (dialog) observer.observe(dialog)
    }
    if (dialog?.getAnimations?.().some(animation => animation.playState === "running")) {
      trackingUntil = performance.now() + 200
    }
    update()
  })

  return {
    destroy() {
      disposed = true
      cancelAnimationFrame(frame)
      observer?.disconnect()
      window.removeEventListener("scroll", schedule, true)
      window.removeEventListener("resize", schedule)
      viewport?.removeEventListener("scroll", schedule)
      viewport?.removeEventListener("resize", schedule)
      dialog?.removeEventListener("animationend", animationFinished)
      dialog?.removeEventListener("animationcancel", animationFinished)
      if (topLayer) { try { node.hidePopover() } catch { /* Already removed by the owner. */ } }
      if (dialog && previousOverflow !== undefined) dialog.style.overflow = previousOverflow
      // Svelte may remove the original range before destroying the action.
      if (moved) node.remove()
    },
  }
}
