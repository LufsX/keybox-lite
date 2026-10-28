import type { Action } from "svelte/action"

/** Apply to each .target-search, including its loading placeholder. */
export const stickySearch: Action<HTMLElement> = (node) => {
  const main = node.closest("main")
  const toolbar = main?.querySelector<HTMLElement>(".target-toolbar")
  if (!main || !toolbar) return

  let frame = 0
  let disposed = false
  const previousHeight = main.style.getPropertyValue("--targets-search-height")
  const previousPriority = main.style.getPropertyPriority("--targets-search-height")

  const measure = () => {
    frame = 0
    if (disposed) return
    const active = node.isConnected && toolbar.isConnected && toolbar.classList.contains("target-toolbar")
    const searchRect = node.getBoundingClientRect()
    if (active) {
      const height = `${searchRect.height}px`
      if (main.style.getPropertyValue("--targets-search-height") !== height) {
        main.style.setProperty("--targets-search-height", height)
      }
    }
    main.classList.toggle("targets-search-docked", active && searchRect.height > 0 && searchRect.top <= toolbar.getBoundingClientRect().bottom + 0.5)
  }
  const schedule = () => {
    if (!disposed && !frame) frame = requestAnimationFrame(measure)
  }
  const resize = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(schedule)
  resize?.observe(node)
  resize?.observe(toolbar)
  resize?.observe(main)
  const mutation = new MutationObserver(schedule)
  mutation.observe(toolbar, { attributes: true, attributeFilter: ["class"] })
  window.addEventListener("scroll", schedule, { passive: true, capture: true })
  window.addEventListener("resize", schedule, { passive: true })
  schedule()

  return {
    destroy() {
      disposed = true
      cancelAnimationFrame(frame)
      resize?.disconnect()
      mutation.disconnect()
      window.removeEventListener("scroll", schedule, true)
      window.removeEventListener("resize", schedule)
      main.classList.remove("targets-search-docked")
      if (previousHeight) main.style.setProperty("--targets-search-height", previousHeight, previousPriority)
      else main.style.removeProperty("--targets-search-height")
    },
  }
}
