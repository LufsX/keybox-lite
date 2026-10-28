<script lang="ts">
  import { onMount, type Snippet } from "svelte"
  import { ui } from "../ui"

  interface Props {
    title: string
    description?: string
    closeLabel?: string
    busy?: boolean
    onclose: () => void
    children: Snippet
    footer?: Snippet<[requestClose: () => void]>
    hideFooter?: boolean
    contentElement?: HTMLDivElement
  }

  let {
    title, description, closeLabel = "Close", busy = false, onclose, children, footer, hideFooter = false,
    contentElement = $bindable(),
  }: Props = $props()

  const id = $props.id()
  let dialog: HTMLDialogElement
  let closeButton: HTMLButtonElement
  let closing = $state(false)
  let reducedMotion = $state(false)
  let timer: ReturnType<typeof setTimeout> | undefined
  let disposed = false
  let completed = false
  let backdropPointer: number | undefined
  let restoreFocus = () => {}

  function cancelClose() {
    clearTimeout(timer)
    timer = undefined
    closing = false
  }

  function finishClose() {
    if (disposed || completed || !closing) return
    if (busy) {
      cancelClose()
      return
    }
    clearTimeout(timer)
    timer = undefined
    completed = true
    dialog.close()
    restoreFocus()
    onclose()
  }

  function requestClose() {
    if (busy || closing || completed || disposed) return
    closing = true
    if (reducedMotion) finishClose()
    else timer = setTimeout(finishClose, 150)
  }

  function cancel(event: Event) {
    event.preventDefault()
    requestClose()
  }

  function isBackdrop(event: PointerEvent) {
    if (event.target !== dialog) return false
    const rect = dialog.getBoundingClientRect()
    return event.clientX < rect.left || event.clientX > rect.right ||
      event.clientY < rect.top || event.clientY > rect.bottom
  }

  function pointerdown(event: PointerEvent) {
    backdropPointer = event.isPrimary && event.button === 0 && isBackdrop(event) ? event.pointerId : undefined
  }

  function pointerup(event: PointerEvent) {
    const dismiss = backdropPointer === event.pointerId && isBackdrop(event)
    backdropPointer = undefined
    if (dismiss) requestClose()
  }

  $effect(() => {
    if (busy && closing) cancelClose()
  })

  onMount(() => {
    const previousFocus = document.activeElement
    let restored = false
    restoreFocus = () => {
      if (restored) return
      restored = true
      if (previousFocus instanceof HTMLElement && previousFocus.isConnected) previousFocus.focus({ preventScroll: true })
    }
    const previousOverflow = document.body.style.overflow
    const media = typeof window.matchMedia === "function" ? window.matchMedia("(prefers-reduced-motion: reduce)") : undefined
    reducedMotion = media?.matches ?? false
    const motionChanged = () => {
      reducedMotion = media?.matches ?? false
      if (reducedMotion && closing) finishClose()
    }
    media?.addEventListener?.("change", motionChanged)
    document.body.style.overflow = "hidden"
    try {
      dialog.showModal()
      if (busy) dialog.focus({ preventScroll: true })
      else closeButton.focus({ preventScroll: true })
    } catch (error) {
      document.body.style.overflow = previousOverflow
      media?.removeEventListener?.("change", motionChanged)
      restoreFocus()
      throw error
    }
    return () => {
      disposed = true
      clearTimeout(timer)
      media?.removeEventListener?.("change", motionChanged)
      if (dialog.open) dialog.close()
      document.body.style.overflow = previousOverflow
      restoreFocus()
    }
  })
</script>

<dialog
  bind:this={dialog}
  class="modal"
  aria-labelledby={id + "-title"}
  aria-describedby={description ? id + "-description" : undefined}
  aria-busy={busy}
  tabindex="-1"
  data-closing={closing}
  data-reduced-motion={reducedMotion}
  oncancel={cancel}
  onkeydown={(event) => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); requestClose() } }}
  onpointerdown={pointerdown}
  onpointerup={pointerup}
  onpointercancel={() => { backdropPointer = undefined }}
>
  <header>
    <div class="heading">
      <h2 id={id + "-title"}>{title}</h2>
      {#if description}<p id={id + "-description"}>{description}</p>{/if}
    </div>
    <button bind:this={closeButton} type="button" class={"modal-close " + ui.iconButton} aria-label={closeLabel} title={closeLabel} disabled={busy || closing} onclick={requestClose}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
    </button>
  </header>
  <div class="body" data-modal-scroll bind:this={contentElement}>
    {@render children()}
  </div>
  {#if footer && !hideFooter}
    <footer>{@render footer(requestClose)}</footer>
  {/if}
</dialog>

<style>
  .modal {
    box-sizing: border-box;
    position: fixed;
    inset: calc(env(safe-area-inset-top) + var(--page-gutter)) calc(env(safe-area-inset-right) + var(--page-gutter)) calc(env(safe-area-inset-bottom) + var(--page-gutter)) calc(env(safe-area-inset-left) + var(--page-gutter));
    width: min(480px, calc(100vw - env(safe-area-inset-left) - env(safe-area-inset-right) - 2 * var(--page-gutter)));
    max-width: none;
    max-height: calc(100vh - env(safe-area-inset-top) - env(safe-area-inset-bottom) - 2 * var(--page-gutter));
    max-height: calc(100dvh - env(safe-area-inset-top) - env(safe-area-inset-bottom) - 2 * var(--page-gutter));
    margin: auto;
    padding: 0;
    overflow: hidden;
    border: 1px solid var(--line);
    border-radius: 10px;
    background: var(--panel);
    color: var(--text);
    font-size: 12px;
    line-height: 1.5;
    box-shadow: 0 16px 48px #00000030;
    animation: modal-enter 150ms ease-out both;
  }
  .modal[open] { display: flex; flex-direction: column; }
  .modal::backdrop { background: #00000066; animation: backdrop-enter 150ms ease-out both; }
  header {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 6px;
    padding: 6px 10px;
    border-bottom: 1px solid var(--line);
  }
  .heading { flex: 1; min-width: 0; }
  h2 { margin: 0; font-size: 14px; font-weight: 600; overflow-wrap: anywhere; }
  .modal-close { width: 28px; height: 28px; padding: 4px; border-radius: 6px; }
  p { margin: 2px 0 0; color: var(--muted); overflow-wrap: anywhere; }
  button:disabled { opacity: .48; cursor: default; }
  .body { flex: 1 1 auto; min-height: 0; padding: var(--content-padding); overflow: auto; overscroll-behavior: contain; scroll-padding-top: calc(32px + 2 * var(--content-padding)); scrollbar-width: thin; scrollbar-color: var(--line) transparent; }
  footer {
    display: flex;
    flex: 0 0 auto;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 6px;
    padding: 6px 10px;
    border-top: 1px solid var(--line);
    background: var(--inset);
  }
  footer :global(button) { min-height: 32px; min-width: 64px; padding: 4px 10px; border-radius: 6px; font-size: 12px; }
  .modal[data-closing="true"] { animation: modal-exit 150ms ease-in both; }
  .modal[data-closing="true"]::backdrop { animation: backdrop-exit 150ms ease-in both; }
  .modal[data-reduced-motion="true"], .modal[data-reduced-motion="true"]::backdrop { animation-duration: 0ms; }
  @media (prefers-reduced-motion: reduce) {
    .modal, .modal::backdrop { animation-duration: 0ms; }
  }
  @keyframes modal-enter { from { opacity: 0; transform: translateY(8px) scale(.98); } to { opacity: 1; transform: none; } }
  @keyframes modal-exit { from { opacity: 1; transform: none; } to { opacity: 0; transform: translateY(6px) scale(.98); } }
  @keyframes backdrop-enter { from { opacity: 0; } to { opacity: 1; } }
  @keyframes backdrop-exit { from { opacity: 1; } to { opacity: 0; } }
</style>
