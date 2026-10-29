<script lang="ts">
  import { onMount, type Snippet } from "svelte"
  import { ui } from "../ui"
  import { registerModalBack } from "../modalBack"

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
  let content: HTMLDivElement
  let heading: HTMLElement
  let actions: HTMLElement | undefined = $state()
  let resizeFrame = 0
  let observer: ResizeObserver | undefined
  let closeButton: HTMLButtonElement
  let closing = $state(false)
  let reducedMotion = $state(false)
  let timer: ReturnType<typeof setTimeout> | undefined
  let disposed = false
  let completed = false
  let backdropPointer: number | undefined
  let restoreFocus = () => {}
  let releaseBack: () => Promise<void> = () => Promise.resolve()

  function cancelClose() {
    clearTimeout(timer)
    timer = undefined
    closing = false
  }

  async function finishClose() {
    if (disposed || completed || !closing) return
    if (busy) {
      cancelClose()
      return
    }
    clearTimeout(timer)
    timer = undefined
    completed = true
    dialog.close()
    await releaseBack()
    if (disposed) return
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
    let removeResize = () => {}
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
      releaseBack = registerModalBack(() => {
        if (busy) return false
        requestClose()
        return true
      })
      const resize = () => {
        cancelAnimationFrame(resizeFrame)
        resizeFrame = requestAnimationFrame(() => {
          if (disposed || !dialog.open || closing) return
          const body = getComputedStyle(contentElement!)
          const style = getComputedStyle(dialog)
          const wanted = heading.offsetHeight + (actions?.offsetHeight ?? 0) + content.offsetHeight +
            parseFloat(body.paddingTop) + parseFloat(body.paddingBottom) +
            parseFloat(style.borderTopWidth) + parseFloat(style.borderBottomWidth)
          const height = Math.min(wanted, parseFloat(style.maxHeight))
          if (Number.isFinite(height) && Math.abs(parseFloat(dialog.style.height || "0") - height) > 0.5) {
            dialog.style.height = height + "px"
          }
        })
      }
      window.addEventListener("resize", resize)
      window.visualViewport?.addEventListener("resize", resize)
      removeResize = () => {
        window.removeEventListener("resize", resize)
        window.visualViewport?.removeEventListener("resize", resize)
      }
      observer = new ResizeObserver(resize)
      observer.observe(content)
      observer.observe(heading)
      if (actions) observer.observe(actions)
      resize()
      if (busy) dialog.focus({ preventScroll: true })
      else closeButton.focus({ preventScroll: true })
    } catch (error) {
      releaseBack()
      document.body.style.overflow = previousOverflow
      media?.removeEventListener?.("change", motionChanged)
      restoreFocus()
      throw error
    }
    return () => {
      disposed = true
      releaseBack()
      clearTimeout(timer)
      observer?.disconnect()
      removeResize()
      cancelAnimationFrame(resizeFrame)
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
  <header bind:this={heading}>
    <div class="heading">
      <h2 id={id + "-title"}>{title}</h2>
      {#if description}<p id={id + "-description"}>{description}</p>{/if}
    </div>
    <button bind:this={closeButton} type="button" class={"modal-close " + ui.iconButton} aria-label={closeLabel} title={closeLabel} disabled={busy || closing} onclick={requestClose}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
    </button>
  </header>
  <div class="body" data-modal-scroll bind:this={contentElement}>
    <div class="content" bind:this={content}>{@render children()}</div>
  </div>
  {#if footer && !hideFooter}
    <footer bind:this={actions}>{@render footer(requestClose)}</footer>
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
    transition: height 180ms cubic-bezier(.2, .7, .2, 1);
  }
  .modal[open] { display: flex; flex-direction: column; }
  .modal::backdrop { background: #00000066; animation: backdrop-enter 150ms ease-out both; }
  header {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 6px;
    padding: 8px var(--content-padding);
    border-bottom: 1px solid var(--line);
  }
  .heading { flex: 1; min-width: 0; }
  h2 { margin: 0; font-size: 14px; font-weight: 600; overflow-wrap: anywhere; }
  .modal-close { width: 28px; height: 28px; padding: 4px; border-radius: 6px; }
  p { margin: 2px 0 0; color: var(--muted); overflow-wrap: anywhere; }
  button:disabled { opacity: .48; cursor: default; }
  .body { flex: 1 1 auto; min-height: 0; padding: 14px var(--content-padding); overflow: auto; overscroll-behavior: contain; scroll-padding-top: calc(32px + 2 * var(--content-padding)); scrollbar-width: thin; scrollbar-color: var(--line) transparent; }
  footer {
    display: flex;
    flex: 0 0 auto;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 6px;
    padding: 8px var(--content-padding);
    border-top: 1px solid var(--line);
    background: var(--inset);
  }
  .content { min-width: 0; }
  .modal-close, footer :global(button) {
    border: 1px solid var(--line);
    background: var(--glass-inset, var(--inset));
    box-shadow: inset 0 1px 0 var(--glass-highlight, transparent);
  }
  footer :global(button) {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    min-height: 32px;
    min-width: 0;
    padding: 4px 10px;
    border-radius: 6px;
    font-size: 12px;
    font-weight: 550;
    line-height: 1.5;
  }
  .modal-close:hover:enabled, footer :global(button:hover:enabled) { border-color: var(--accent); background: var(--accent-bg); }
  @supports ((backdrop-filter: blur(1px)) or (-webkit-backdrop-filter: blur(1px))) and (background: color-mix(in srgb, #fff 50%, transparent)) {
    .modal {
      isolation: isolate;
      border-color: var(--glass-border, var(--line));
      background: transparent;
      box-shadow: var(--glass-dialog-shadow, 0 16px 48px #00000030);
    }
    .modal::before {
      content: "";
      position: absolute;
      inset: 0;
      z-index: -1;
      border-radius: inherit;
      background: var(--glass-floating, var(--panel));
      box-shadow: inset 0 1px 0 var(--glass-highlight, transparent);
      -webkit-backdrop-filter: blur(var(--glass-blur, 24px));
      backdrop-filter: blur(var(--glass-blur, 24px));
      pointer-events: none;
    }
    .modal::backdrop {
      -webkit-backdrop-filter: blur(3px);
      backdrop-filter: blur(3px);
    }
    header {
      border-bottom-color: var(--glass-border, var(--line));
      background: color-mix(in srgb, var(--panel) 20%, transparent);
    }
    footer {
      border-top-color: var(--glass-border, var(--line));
      background: color-mix(in srgb, var(--inset) 65%, transparent);
    }
  }
  @media (prefers-reduced-transparency: reduce), (prefers-contrast: more) {
    .modal {
      border-color: var(--line);
      background: var(--panel);
      box-shadow: var(--glass-dialog-shadow, 0 16px 48px #00000030);
    }
    .modal::before { content: none; -webkit-backdrop-filter: none; backdrop-filter: none; }
    .modal::backdrop { -webkit-backdrop-filter: none; backdrop-filter: none; }
    header { border-bottom-color: var(--line); background: transparent; }
    footer { border-top-color: var(--line); background: var(--inset); }
  }
  .modal[data-closing="true"] { animation: modal-exit 150ms ease-in both; }
  .modal[data-closing="true"]::backdrop { animation: backdrop-exit 150ms ease-in both; }
  .modal[data-reduced-motion="true"], .modal[data-reduced-motion="true"]::backdrop { animation-duration: 0ms; }
  @media (prefers-reduced-motion: reduce) {
    .modal { transition: none; }
    .modal, .modal::backdrop { animation-duration: 0ms; }
  }
  @keyframes modal-enter { from { opacity: 0; transform: translateY(8px) scale(.98); } to { opacity: 1; transform: none; } }
  @keyframes modal-exit { from { opacity: 1; transform: none; } to { opacity: 0; transform: translateY(6px) scale(.98); } }
  @keyframes backdrop-enter { from { opacity: 0; } to { opacity: 1; } }
  @keyframes backdrop-exit { from { opacity: 1; } to { opacity: 0; } }
</style>
