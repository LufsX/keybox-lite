export const ui = {
  panel: "min-w-0 rounded-[14px] border border-[var(--line)] bg-[var(--panel)] p-[var(--content-padding)]",
  panelRow: "flex min-h-11 w-full items-center justify-between gap-2 p-[var(--content-padding)] text-start text-sm font-semibold",
  selectButton: "flex h-8 max-w-full min-w-0 items-center justify-between gap-1.5 rounded-lg border border-[var(--line)] bg-[var(--inset)] px-2 py-1 text-xs font-[550] text-[var(--text)] transition-colors duration-150 hover:enabled:border-[var(--accent)] disabled:opacity-50",
  heading: "mb-3 flex min-h-[22px] items-center justify-between gap-2",
  headingTitle: "flex min-w-0 items-center gap-1.5 text-sm font-semibold [&_svg]:text-[var(--muted)]",
  textButton: "inline-flex min-h-8 shrink-0 items-center justify-center gap-1.5 rounded-lg px-2 py-1 text-[11px] leading-normal text-[var(--accent)] transition-colors duration-150 hover:enabled:bg-[var(--inset)] disabled:opacity-50",
  button: "flex min-h-10 min-w-0 items-center justify-center gap-1.5 rounded-[10px] px-3 py-1.5 text-[13px] font-semibold leading-normal transition duration-150 active:enabled:scale-[.98] disabled:opacity-[.48]",
  iconButton: "grid size-10 shrink-0 place-items-center rounded-[10px] p-2 text-[var(--muted)] transition-colors duration-150 hover:bg-[var(--inset)] hover:text-[var(--text)]",
  buttonGroup: "gap-2",
  menu: "absolute right-0 top-[calc(100%+4px)] z-20 grid w-[120px] max-w-[calc(100vw-24px)] gap-0.5 rounded-[10px] border border-[var(--line)] bg-[var(--panel)] p-0.5 shadow-[0_10px_32px_#00000026]",
  menuItem: "flex min-h-8 min-w-0 items-center gap-1.5 rounded-lg px-1.5 py-1 text-start text-xs leading-normal text-[var(--muted)] [overflow-wrap:anywhere] transition-colors duration-150 hover:bg-[var(--inset)] [&_span]:min-w-0 [&_span]:flex-1",
  menuItemActive: "bg-[var(--inset)] font-semibold !text-[var(--accent)]",
} as const
