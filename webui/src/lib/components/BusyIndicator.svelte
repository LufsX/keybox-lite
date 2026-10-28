<script lang="ts">
  import { onDestroy } from "svelte"
  import { Loader2 } from "lucide-svelte"

  let { active, label, visible = $bindable(false) }: { active: boolean; label: string; visible?: boolean } = $props()
  let shownAt = 0
  let showTimer: ReturnType<typeof setTimeout> | undefined
  let hideTimer: ReturnType<typeof setTimeout> | undefined

  $effect(() => {
    const busy = active
    clearTimeout(showTimer)
    clearTimeout(hideTimer)
    if (busy) {
      if (!visible) showTimer = setTimeout(() => { shownAt = Date.now(); visible = true }, 160)
    } else if (visible) {
      hideTimer = setTimeout(() => { visible = false }, Math.max(0, 320 - (Date.now() - shownAt)))
    }
    return () => { clearTimeout(showTimer); clearTimeout(hideTimer) }
  })
  onDestroy(() => { clearTimeout(showTimer); clearTimeout(hideTimer) })
</script>

<span class="busy-status" class:is-visible={visible} role="status" aria-label={visible ? label : undefined}>
  <span class="busy-content" aria-hidden="true"><Loader2 size={13} class={visible ? "motion-safe:animate-spin" : ""} />{label}</span>
</span>

<style>
  .busy-status { display: inline-flex; min-height: 18px; align-items: center; white-space: nowrap; color: var(--muted); font-size: 11px; }
  .busy-content { display: inline-flex; align-items: center; gap: 6px; opacity: 0; transition: opacity 140ms ease-out; }
  .is-visible .busy-content { opacity: 1; }
  @media (prefers-reduced-motion: reduce) { .busy-content { transition: none; } }
</style>
