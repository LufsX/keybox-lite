<script lang="ts">
  import { ShieldAlert, X } from "lucide-svelte"
  import { cubicOut } from "svelte/easing"

  let { message, closeLabel, active = true, onclose }: {
    message: string
    closeLabel: string
    active?: boolean
    onclose: () => void
  } = $props()

  let hovered = $state(false)
  let focused = $state(false)

  $effect(() => {
    const text = message
    if (!active || !text || text.length > 180 || hovered || focused) return
    const timer = setTimeout(onclose, 8000)
    return () => clearTimeout(timer)
  })

  function motion(_node: HTMLElement) {
    return {
      duration: !active || window.matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : 160,
      easing: cubicOut,
      css: (t: number) => `opacity:${t};transform:translateY(${(1 - t) * 6}px)`,
    }
  }
</script>

{#if active && message}
  <aside class="app-toast" role="alert" aria-atomic="true" in:motion out:motion
    onpointerenter={() => hovered = true} onpointerleave={() => hovered = false}
    onfocusin={() => focused = true}
    onfocusout={(event) => focused = event.relatedTarget instanceof Node && event.currentTarget.contains(event.relatedTarget)}>
    <ShieldAlert size={18} class="toast-icon" aria-hidden="true" />
    <p class="toast-message">{message}</p>
    <button type="button" class="toast-close" aria-label={closeLabel} onclick={onclose}><X size={16} /></button>
  </aside>
{/if}

<style>
  .app-toast {
    position: fixed;
    left: var(--page-edge-left);
    right: var(--page-edge-right);
    bottom: calc(env(safe-area-inset-bottom) + var(--page-gutter));
    z-index: 80;
    display: grid;
    grid-template-columns: 18px minmax(0, 1fr) 32px;
    align-items: start;
    gap: 8px;
    padding: 10px;
    border: 1px solid var(--line);
    border-inline-start: 3px solid var(--red);
    border-radius: 12px;
    background: var(--panel);
    color: var(--text);
    box-shadow: 0 8px 28px #00000026;
  }
  .app-toast :global(.toast-icon) { margin-top: 7px; color: var(--red); }
  .toast-message {
    max-height: min(34dvh, 240px);
    margin: 0;
    padding-block: 6px;
    overflow: auto;
    overscroll-behavior: contain;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    font-size: 12px;
    line-height: 1.6;
    scrollbar-width: thin;
    scrollbar-color: var(--line) transparent;
  }
  .toast-close {
    display: grid;
    width: 32px;
    height: 32px;
    place-items: center;
    border-radius: 8px;
    color: var(--muted);
    background: var(--inset);
    transition: background-color 150ms, color 150ms;
  }
  .toast-close:hover { background: var(--accent-bg); color: var(--accent); }
  .toast-close:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  @media (prefers-reduced-motion: reduce) {
    .toast-close { transition: none; }
  }
</style>
