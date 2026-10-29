<script lang="ts">
  import { X } from "lucide-svelte"
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
    if (!active || !message) {
      hovered = false
      focused = false
    }
  })

  $effect(() => {
    const text = message
    if (!active || !text || text.length > 180 || hovered || focused) return
    const timer = setTimeout(onclose, 8000)
    return () => clearTimeout(timer)
  })

  function motion(_node: HTMLElement) {
    return {
      duration: !active || window.matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : 180,
      easing: cubicOut,
      css: (t: number) => `opacity:${t};transform:translateY(${(1 - t) * 10}px) scale(${.98 + t * .02})`,
    }
  }
</script>

{#if active && message}
  <div class="toast-position">
    <aside class="toast" role="alert" aria-atomic="true" in:motion out:motion
      onpointerenter={() => hovered = true} onpointerleave={() => hovered = false}
      onfocusin={() => focused = true}
      onfocusout={(event) => focused = event.relatedTarget instanceof Node && event.currentTarget.contains(event.relatedTarget)}>
      <p class="message">{message}</p>
      <button type="button" class="close" aria-label={closeLabel} title={closeLabel} onclick={onclose}>
        <X size={15} strokeWidth={1.8} aria-hidden="true" />
      </button>
    </aside>
  </div>
{/if}

<style>
  .toast-position {
    position: fixed;
    inset-inline: var(--page-edge-left) var(--page-edge-right);
    bottom: calc(env(safe-area-inset-bottom) + 20px);
    z-index: 80;
    display: flex;
    justify-content: center;
    pointer-events: none;
  }
  .toast {
    --toast-bg: #263249;
    --toast-text: #f1f4fa;
    --toast-hover: #ffffff14;
    box-sizing: border-box;
    display: flex;
    align-items: center;
    gap: 12px;
    width: fit-content;
    max-width: min(100%, 440px);
    padding: 10px 10px 10px 16px;
    border: 1px solid #ffffff0d;
    border-radius: 16px;
    background: var(--toast-bg);
    color: var(--toast-text);
    box-shadow: 0 4px 12px #00000014, 0 12px 32px #0000001f;
    pointer-events: auto;
  }
  :global(.dark) .toast {
    --toast-bg: #e2e8f3;
    --toast-text: #202b42;
    --toast-hover: #202b4210;
    border-color: #ffffff26;
  }
  .message {
    flex: 1;
    min-width: 0;
    max-height: min(30dvh, 220px);
    margin: 0;
    padding-block: 2px;
    overflow: auto;
    overscroll-behavior: contain;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    font-size: 13px;
    font-weight: 450;
    line-height: 1.6;
    scrollbar-width: thin;
    scrollbar-color: currentColor transparent;
  }
  .close {
    display: grid;
    width: 32px;
    height: 32px;
    flex: 0 0 32px;
    align-self: flex-start;
    place-items: center;
    padding: 0;
    border: 0;
    border-radius: 50%;
    background: transparent;
    color: inherit;
    opacity: .65;
    transition: background-color 150ms, opacity 150ms, transform 150ms;
  }
  .close:active { transform: scale(.92); background: var(--toast-hover); opacity: 1; }
  .close:focus-visible { outline: 2px solid currentColor; outline-offset: -2px; opacity: 1; }
  @media (hover: hover) {
    .close:hover { background: var(--toast-hover); opacity: 1; }
  }
  @supports ((backdrop-filter: blur(1px)) or (-webkit-backdrop-filter: blur(1px))) and (background: color-mix(in srgb, #fff 50%, transparent)) {
    .toast {
      background: color-mix(in srgb, var(--toast-bg) 92%, transparent);
      -webkit-backdrop-filter: blur(var(--glass-blur, 24px));
      backdrop-filter: blur(var(--glass-blur, 24px));
      box-shadow: var(--glass-float-shadow, 0 4px 12px #00000014, 0 12px 32px #0000001f), inset 0 1px 0 var(--glass-highlight, transparent);
    }
  }
  @media (prefers-reduced-transparency: reduce), (prefers-contrast: more) {
    .toast {
      background: var(--toast-bg);
      -webkit-backdrop-filter: none;
      backdrop-filter: none;
      box-shadow: var(--glass-float-shadow, 0 4px 12px #00000014, 0 12px 32px #0000001f);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .close { transition: none; }
  }
</style>
