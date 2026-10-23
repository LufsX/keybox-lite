import { cubicOut } from "svelte/easing"

function duration(value = 160) {
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : value
}

export function surface(_node: HTMLElement, { y = 4, scale = .98, duration: time = 160 } = {}) {
  return {
    duration: duration(time),
    easing: cubicOut,
    css: (t: number) => `opacity:${t};transform:translateY(${(1 - t) * y}px) scale(${scale + (1 - scale) * t})`,
  }
}

export function reveal(node: HTMLElement) {
  const width = node.getBoundingClientRect().width
  return {
    duration: duration(180),
    easing: cubicOut,
    css: (t: number) => `width:${width * t}px;margin-inline-end:${8 * t}px;opacity:${t};overflow:hidden;transform:translateX(${(1 - t) * 6}px)`,
  }
}

// Retain the outgoing page visually without keeping its height in the document.
export function pageExit(node: HTMLElement) {
  const bounds = node.getBoundingClientRect()
  const header = document.querySelector("main > header")?.getBoundingClientRect()
  const clip = Math.max(0, (header?.bottom ?? 0) - bounds.top)
  const opacity = parseFloat(getComputedStyle(node).opacity)
  return {
    duration: duration(140),
    easing: cubicOut,
    css: (t: number) => `position:fixed;left:${bounds.left}px;top:${bounds.top}px;width:${bounds.width}px;height:${bounds.height}px;z-index:25;pointer-events:none;clip-path:inset(${clip}px 0 0);opacity:${t * opacity}`,
  }
}
