<script lang="ts">
  import { onMount } from "svelte"

  let { text }: { text: string } = $props()
  let viewport = $state<HTMLSpanElement>()
  let content = $state<HTMLSpanElement>()
  let distance = $state(0)
  let visible = $state(false)
  let reduced = $state(true)
  let hovered = false
  let focused = false
  let animation: Animation | undefined

  onMount(() => {
    const node = viewport!, label = content!
    const media = window.matchMedia("(prefers-reduced-motion: reduce)")
    const syncMotion = () => reduced = media.matches
    const measure = () => {
      const overflow = label.getBoundingClientRect().width - node.clientWidth
      distance = overflow > 1 ? Math.ceil(overflow) + 10 : 0
    }
    const pause = () => animation?.pause()
    const resume = () => { if (!hovered && !focused) animation?.play() }
    const enter = () => { hovered = true; pause() }
    const leave = () => { hovered = false; resume() }
    const focus = () => { focused = true; pause() }
    const blur = () => { focused = false; resume() }
    const button = node.closest("button")
    const resize = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(measure)
    resize?.observe(node)
    resize?.observe(label)
    const intersection = typeof IntersectionObserver === "undefined" ? undefined : new IntersectionObserver(entries => {
      visible = entries[0].isIntersecting
    })
    intersection?.observe(node)
    if (!intersection) visible = true
    node.addEventListener("pointerenter", enter)
    node.addEventListener("pointerleave", leave)
    button?.addEventListener("focus", focus)
    button?.addEventListener("blur", blur)
    window.addEventListener("resize", measure, { passive: true })
    media.addEventListener("change", syncMotion)
    syncMotion()
    measure()
    return () => {
      resize?.disconnect()
      intersection?.disconnect()
      node.removeEventListener("pointerenter", enter)
      node.removeEventListener("pointerleave", leave)
      button?.removeEventListener("focus", focus)
      button?.removeEventListener("blur", blur)
      window.removeEventListener("resize", measure)
      media.removeEventListener("change", syncMotion)
    }
  })

  $effect(() => {
    const label = content
    const amount = distance
    if (!label || !visible || reduced || !amount) return
    const travel = Math.max(1500, amount / 26 * 1000)
    const pause = 1200
    const duration = 2 * travel + 2 * pause
    const current = label.animate([
      { transform: "translateX(0)", offset: 0 },
      { transform: "translateX(0)", offset: pause / duration },
      { transform: `translateX(-${amount}px)`, offset: (pause + travel) / duration },
      { transform: `translateX(-${amount}px)`, offset: (2 * pause + travel) / duration },
      { transform: "translateX(0)", offset: 1 },
    ], { duration, iterations: Infinity, easing: "linear" })
    animation = current
    if (hovered || focused) current.pause()
    return () => {
      current.cancel()
      if (animation === current) animation = undefined
    }
  })
</script>

<span bind:this={viewport} dir="ltr" class="overflow-text font-mono text-[11px] leading-4 text-[var(--muted)]"
  class:is-overflowing={distance > 0} title={text}>
  <span bind:this={content} class="overflow-text-content">{text}</span>
</span>

<style>
  .overflow-text { display: block; min-width: 0; width: 100%; overflow: hidden; white-space: nowrap; }
  .overflow-text-content { display: inline-block; width: max-content; vertical-align: top; }
  .is-overflowing { mask-image: linear-gradient(to right, #000 calc(100% - 10px), transparent); }
  @media (prefers-reduced-motion: reduce) {
    .overflow-text { overflow-x: auto; scrollbar-width: none; mask-image: none; }
    .overflow-text::-webkit-scrollbar { display: none; }
  }
</style>
