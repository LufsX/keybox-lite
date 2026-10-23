<script lang="ts">
  import { onDestroy, untrack } from "svelte"
  import { Search, KeyRound, ChevronRight, NotebookPen, Undo2, PackagePlus, RefreshCw, ListPlus, SlidersHorizontal, SquareDashed, CheckCheck, Settings2 } from "lucide-svelte"
  import { packagesInfo } from "../apps"
  import AppRow from "./AppRow.svelte"
  import Modal from "./Modal.svelte"
  import VirtualList from "./VirtualList.svelte"
  import { exec, type ExecResult } from "../bridge"
  import { messages, type Locale } from "../i18n"
  import { ui } from "../ui"
  import {
    modes, policyKeys, validPackage, validDraft, validPolicy, validPolicyValue, parseTargetStatus, parseTargetAck,
    draftFromStatus, targetStatusCommand, targetSaveCommand, targetEntryCommand, visiblePackages,
    type TargetStatus, type TargetDraft, type TargetMode, type PolicyKey, type Policy,
  } from "../targets"

  let { bin, lang, disabled = false, onMenuChange, onTitleVisibility }: { bin: string; lang: Locale; disabled?: boolean; onMenuChange?: (open: boolean) => void; onTitleVisibility?: (visible: boolean) => void } = $props()
  let m = $derived(messages[lang])
  let status = $state<TargetStatus | null>(null)
  let draft = $state<TargetDraft | null>(null)
  let baseline = $state("")
  let ready = $state(false)
  let entryReady = $state(false)
  let pending = $state(false)
  let error = $state("")
  let notice = $state("")
  let query = $state("")
  let filter = $state("listed")
  let manual = $state("")
  let manualMode = $state<TargetMode>("auto")
  let editor = $state<{ name: string; mode: TargetMode; policy: Policy; tracked: boolean } | null>(null)
  let editorBaseline = ""
  let menuOpen = $state(false)
  let menuElement = $state<HTMLDivElement>()
  let sheet = $state<"system" | "global" | "manual" | "entry" | null>(null)
  let systemQuery = $state("")
  let systemScroll = $state<HTMLDivElement>()
  let globalPolicy = $state<Policy>({})
  let globalBaseline = ""
  let systemDraft = $state<string[]>([])
  let systemBaseline = ""
  let systemOrder = $state<string[]>([])
  let metadata = $state<Map<string, { label: string; icon: string | null }>>(new Map())
  let metadataSequence = 0
  let metadataNames = $derived(status ? [...new Set([...status.packages.map(item => item.package_name), ...(draft?.targets.map(item => item.package_name) ?? [])])].sort().join("\n") : "")
  let systemRows = $derived(orderedSystemRows())
  function orderedSystemRows() {
    if (!status || !draft) return []
    const order = new Map(systemOrder.map((name, index) => [name, index]))
    return visiblePackages(status, draft, systemQuery, "system", metadata)
      .sort((a, b) => (order.get(a.package_name) ?? 0) - (order.get(b.package_name) ?? 0))
  }
  let fabCollapsed = $state(false)
  let previousScroll = 0
  let scrollDirection = 0
  let scrollDistance = 0
  let changeCount = $derived(countChanges())
  let sequence = 0
  let disposed = false
  let contextBin = (() => bin)()
  let locked = $derived(disabled || pending || !bin)
  let editable = $derived(!locked && ready && !!status?.supported && !!status?.backend?.active)
  let dirty = $derived(!!draft && JSON.stringify(draft) !== baseline)
  let visible = $derived(status && draft ? visiblePackages(status, draft, query, filter, metadata) : [])
  const inputClass = "min-h-8 min-w-0 rounded-lg border border-[var(--line)] bg-[var(--inset)] px-2 py-1 text-xs text-[var(--text)] disabled:opacity-50"

  $effect(() => {
    if (bin !== contextBin) {
      closeEditor()
      closeSheet()
      menuOpen = false
      contextBin = bin
      sequence++
      pending = false
      ready = false
      entryReady = false
      status = null
      draft = null
      baseline = ""
      error = ""
      notice = ""
    }
    if (disabled) { closeEditor(); closeSheet(); menuOpen = false }
    if (disabled && pending) {
      sequence++
      pending = false
      ready = false
      entryReady = false
    }
  })
  $effect(() => {
    const path = bin, blocked = disabled
    untrack(() => {
      if (path && !blocked && !status && !pending) void refresh(false, true)
    })
  })
  $effect(() => {
    const names = metadataNames, path = bin, blocked = disabled
    return untrack(() => names && path && !blocked ? loadMetadata(names.split("\n"), path) : undefined)
  })
  onDestroy(() => { disposed = true; sequence++; metadataSequence++ })

  function loadMetadata(names: string[], path: string) {
    const id = ++metadataSequence
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), 6000)
    void packagesInfo(names, controller.signal).then(value => {
      if (!disposed && !controller.signal.aborted && id === metadataSequence && bin === path && !disabled) metadata = value
    }).catch(() => {
      if (!disposed && id === metadataSequence && bin === path) metadata = new Map()
    }).finally(() => { clearTimeout(timer) })
    return () => { controller.abort(); clearTimeout(timer); if (id === metadataSequence) metadataSequence++ }
  }
  $effect(() => { onMenuChange?.(menuOpen) })
  function trackTitle(node: HTMLHeadingElement) {
    let observer: IntersectionObserver | undefined
    const toolbar = document.querySelector(".target-toolbar")
    const observe = () => {
      const edge = toolbar?.getBoundingClientRect().bottom ?? 0
      onTitleVisibility?.(node.getBoundingClientRect().bottom <= edge)
      observer?.disconnect()
      if (typeof IntersectionObserver !== "undefined") {
        observer = new IntersectionObserver(([entry]) => onTitleVisibility?.(!entry.isIntersecting), {
          rootMargin: "-" + edge + "px 0px 0px 0px",
        })
        observer.observe(node)
      }
    }
    const resize = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(observe)
    if (toolbar) resize?.observe(toolbar)
    window.addEventListener("resize", observe, { passive: true })
    if (typeof IntersectionObserver === "undefined") window.addEventListener("scroll", observe, { passive: true })
    observe()
    return { destroy() {
      observer?.disconnect()
      resize?.disconnect()
      window.removeEventListener("resize", observe)
      window.removeEventListener("scroll", observe)
      onTitleVisibility?.(false)
    } }
  }
  export function toggleMenu() { menuOpen = !menuOpen }
  function closeMenu(focus = false) {
    menuOpen = false
    if (focus) document.getElementById("targets-menu-trigger")?.focus()
  }
  function dismissMenu(event: Event) {
    const target = event.target
    if (!(target instanceof Element) || target.closest("#targets-menu-trigger")) return
    if (menuOpen && !menuElement?.contains(target)) closeMenu()
  }
  function menuKeydown(event: KeyboardEvent) {
    if (!menuOpen) return
    if (event.key === "Escape") { event.preventDefault(); closeMenu(true); return }
    const buttons = Array.from(menuElement?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)") ?? [])
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key) || !buttons.length) return
    event.preventDefault()
    const index = buttons.indexOf(document.activeElement as HTMLButtonElement)
    const next = event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1 :
      event.key === "ArrowDown" ? (index + 1) % buttons.length : (index <= 0 ? buttons.length : index) - 1
    buttons[next]?.focus()
  }
  function menuAction(action: string) {
    closeMenu(true)
    if (action === "refresh") void refresh()
    else if (action === "select") selectVisible(true)
    else if (action === "clear") {
      if (!status || !draft) return
      if (query.trim()) selectVisible(false)
      else {
        const names = new Set(draft.targets.map(item => item.package_name))
        draft.targets = []
        draft.system_apps = draft.system_apps.filter(name => !names.has(name))
        for (const name of names) delete draft.per_app_policy[name]
        notice = ""
      }
    } else if (action === "auto") autoAdd(!draft?.auto_add_new_apps)
    else if (["system", "global", "manual", "entry"].includes(action)) openSheet(action as NonNullable<typeof sheet>)
  }
  function openSheet(value: NonNullable<typeof sheet>) {
    if (value === "system" && (!canEdit() || !status || !draft)) return
    if (value === "system" && draft && status) {
      systemQuery = ""
      systemDraft = [...draft.system_apps]
      systemBaseline = JSON.stringify(systemDraft)
      const tracked = new Set(systemDraft)
      systemOrder = visiblePackages(status, draft, "", "system").sort((a, b) =>
        Number(tracked.has(b.package_name)) - Number(tracked.has(a.package_name)) || a.package_name.localeCompare(b.package_name)).map(item => item.package_name)
    }
    if (value === "global" && draft) {
      globalPolicy = { ...draft.default_policy }
      globalBaseline = JSON.stringify(globalPolicy)
    }
    sheet = value
  }
  function closeSheet() { sheet = null }
  function sheetChanged() {
    return (sheet === "system" && JSON.stringify(systemDraft) !== systemBaseline) ||
      (sheet === "global" && JSON.stringify(globalPolicy) !== globalBaseline)
  }
  function applyGlobal(close = true) {
    if (!canEdit() || !draft || !validPolicy(globalPolicy)) return
    draft.default_policy = { ...globalPolicy }
    notice = ""
    if (close) closeSheet()
  }
  function editGlobalPolicy(key: PolicyKey, value: string) {
    if (value === "") delete globalPolicy[key]
    else globalPolicy[key] = value
  }
  function toggleSystem(name: string) {
    systemDraft = systemDraft.includes(name) ? systemDraft.filter(item => item !== name) : [...systemDraft, name]
  }
  function applySystem(close = true) {
    if (!canEdit() || !draft) return
    draft.system_apps = [...systemDraft]
    notice = ""
    if (close) closeSheet()
  }
  function discard() {
    if (!canEdit() || !status || !window.confirm(m.targetsDiscard)) return
    accept(status)
    notice = ""
  }
  function onscroll() {
    // Dialog scrolling must not change the page's floating actions.
    if (sheet || editor) return
    const y = Math.max(0, window.scrollY), delta = y - previousScroll
    previousScroll = y
    if (y <= 48) {
      fabCollapsed = false
      scrollDistance = 0
      scrollDirection = 0
      return
    }
    if (Math.abs(delta) < 1) return
    const direction = Math.sign(delta)
    scrollDistance = direction === scrollDirection ? scrollDistance + Math.abs(delta) : Math.abs(delta)
    scrollDirection = direction
    if (scrollDistance >= 24) {
      fabCollapsed = direction > 0
      scrollDistance = 0
    }
  }

  export function canLeave(): boolean {
    if (pending) { notice = m.targetsPending; return false }
    return !(changed() || editorChanged() || sheetChanged()) || window.confirm(m.targetsLeave)
  }

  function modeLabel(mode: TargetMode): string {
    return mode === "auto" ? m.targetsModeAuto : mode === "generate" ? m.targetsModeGenerate : m.targetsModeHack
  }
  function countChanges(): number {
    if (!draft || !baseline) return 0
    const original: TargetDraft = JSON.parse(baseline)
    const names = new Set([...original.targets.map(item => item.package_name), ...draft.targets.map(item => item.package_name),
      ...original.system_apps, ...draft.system_apps, ...Object.keys(original.per_app_policy), ...Object.keys(draft.per_app_policy)])
    const before = new Map(original.targets.map(item => [item.package_name, item.mode]))
    const after = new Map(draft.targets.map(item => [item.package_name, item.mode]))
    const oldSystem = new Set(original.system_apps), newSystem = new Set(draft.system_apps)
    let count = 0
    for (const name of names) {
      if (before.get(name) !== after.get(name) ||
          JSON.stringify(original.per_app_policy[name] ?? {}) !== JSON.stringify(draft.per_app_policy[name] ?? {}) ||
          oldSystem.has(name) !== newSystem.has(name)) count++
    }
    if (JSON.stringify(original.default_policy) !== JSON.stringify(draft.default_policy)) count++
    if (original.auto_add_new_apps !== draft.auto_add_new_apps) count++
    return count
  }
  function editorChanged(): boolean { return !!editor && JSON.stringify(editor) !== editorBaseline }
  function openEditor(name: string) {
    if (!canEdit() || !draft) return
    const target = draft.targets.find(item => item.package_name === name)
    if (!target) return
    editor = { name, mode: target.mode, policy: { ...(draft.per_app_policy[name] ?? {}) }, tracked: draft.system_apps.includes(name) }
    editorBaseline = JSON.stringify(editor)
  }
  function closeEditor() { editor = null }
  function editPolicy(key: PolicyKey, value: string) {
    if (!editor) return
    if (value === "") delete editor.policy[key]
    else editor.policy[key] = value
  }
  function applyEditor(close = true) {
    if (!canEdit() || !draft || !editor || !validPolicy(editor.policy)) return
    setMode(editor.name, editor.mode)
    if (supportsAppPolicy()) {
      if (Object.keys(editor.policy).length) draft.per_app_policy[editor.name] = { ...editor.policy }
      else delete draft.per_app_policy[editor.name]
    }
    trackSystem(editor.name, editor.tracked)
    if (close) closeEditor()
  }

  function changed(): boolean { return !!draft && JSON.stringify(draft) !== baseline }
  function beforeUnload(event: BeforeUnloadEvent) {
    if (changed() || editorChanged() || sheetChanged() || pending) { event.preventDefault(); event.returnValue = "" }
  }
  function live(id: number, path: string): boolean {
    return !disposed && sequence === id && bin === path && !disabled
  }
  function requireSuccess(result: ExecResult) {
    if (result.failure === "unavailable") throw new Error(m.bridgeUnavailable)
    if (result.failure === "timeout") throw new Error(m.commandTimeout)
    if (result.failure || result.errno !== 0) throw new Error(result.stderr.trim() || m.targetsFailed)
  }
  async function readStatus(path: string): Promise<TargetStatus> {
    const result = await exec(targetStatusCommand(path))
    requireSuccess(result)
    try { return parseTargetStatus(result.stdout) } catch { throw new Error(m.targetsInvalid) }
  }
  function accept(value: TargetStatus) {
    status = value
    entryReady = true
    ready = !value.config_error && !value.packages_error
    if (!ready) {
      error = [value.config_error, value.packages_error].filter(Boolean).join("\n")
      return
    }
    draft = draftFromStatus(value)
    if (value.backend?.backend !== "tricky-store") draft.per_app_policy = {}
    baseline = JSON.stringify(draft)
  }
  async function refresh(ask = true, quiet = false) {
    if (disabled || pending || !bin || disposed) return
    if (ask && (changed() || editorChanged() || sheetChanged()) && !window.confirm(m.targetsDiscard)) return
    closeEditor()
    closeSheet()
    const id = ++sequence, path = bin
    pending = true
    ready = false
    entryReady = false
    error = ""
    notice = ""
    try {
      const value = await readStatus(path)
      if (!live(id, path)) return
      accept(value)
      if (ready && !quiet) notice = m.targetsRefreshed
    } catch (cause) {
      if (live(id, path)) error = cause instanceof Error ? cause.message : m.targetsFailed
    } finally {
      if (!disposed && sequence === id) pending = false
    }
  }
  function canEdit(): boolean {
    return !disposed && !disabled && !pending && ready && !!bin && !!status?.supported && !!status?.backend?.active && !!draft
  }
  function select(name: string, enabled: boolean) {
    if (!canEdit() || !draft) return
    if (enabled) {
      if (!draft.targets.some(item => item.package_name === name)) {
        draft.targets.push({ package_name: name, mode: status?.packages.find(item => item.package_name === name)?.mode ?? "auto" })
        if (canTrackSystem(name) && !draft.system_apps.includes(name))
          draft.system_apps.push(name)
      }
    } else {
      draft.targets = draft.targets.filter(item => item.package_name !== name)
      // Remove dependent overrides and tracking only for explicitly removed targets.
      delete draft.per_app_policy[name]
      draft.system_apps = draft.system_apps.filter(item => item !== name)
    }
    notice = ""
  }
  function selectVisible(enabled: boolean) {
    if (!status || !draft) return
    const scope = visiblePackages(status, draft, query, filter, metadata)
    const selected = new Map(draft.targets.map(item => [item.package_name, item]))
    const tracked = new Set(draft.system_apps)
    for (const item of scope) {
      if (enabled) {
        if (!selected.has(item.package_name)) {
          selected.set(item.package_name, { package_name: item.package_name, mode: item.mode })
          if (item.system && !item.package_name.startsWith("uid:")) tracked.add(item.package_name)
        }
      } else {
        selected.delete(item.package_name)
        tracked.delete(item.package_name)
        delete draft.per_app_policy[item.package_name]
      }
    }
    draft.targets = [...selected.values()]
    draft.system_apps = [...tracked]
    notice = ""
  }
  function addManual() {
    if (!canEdit() || !draft) return
    const name = manual.trim()
    if (!validPackage(name)) { error = m.targetsInvalid; return }
    select(name, true)
    setMode(name, manualMode)
    manual = ""
    error = ""
  }
  function setMode(name: string, mode: TargetMode) {
    if (!canEdit() || !draft || !modes.includes(mode)) return
    const target = draft.targets.find(item => item.package_name === name)
    if (target) target.mode = mode
    notice = ""
  }
  function supportsAppPolicy(): boolean { return status?.backend?.backend === "tricky-store" }
  function setPolicy(name: string | null, key: PolicyKey, value: string) {
    if (!canEdit() || !draft || (name !== null && !supportsAppPolicy())) return
    const policy = name === null ? draft.default_policy : (draft.per_app_policy[name] ??= {})
    if (value === "") delete policy[key]
    else policy[key] = value
    if (name !== null && Object.keys(policy).length === 0) delete draft.per_app_policy[name]
    notice = ""
  }
  function canTrackSystem(name: string): boolean {
    return !name.startsWith("uid:") && (!!status?.packages.find(item => item.package_name === name)?.system ||
      !!draft?.system_apps.includes(name))
  }
  function trackSystem(name: string, enabled: boolean) {
    if (!canEdit() || !draft || (enabled && !canTrackSystem(name))) return
    draft.system_apps = draft.system_apps.filter(item => item !== name)
    if (enabled) draft.system_apps.push(name)
    notice = ""
  }
  function autoAdd(enabled: boolean) {
    if (canEdit() && draft) { draft.auto_add_new_apps = enabled; notice = "" }
  }
  async function save() {
    if (!canEdit() || !status || !draft || editor || sheet || !changed()) return
    if (!validDraft(draft)) { error = m.targetsInvalid; return }
    const id = ++sequence, path = bin
    const command = targetSaveCommand(path, status.revision, { ...draft, per_app_policy: supportsAppPolicy() ? draft.per_app_policy : {} })
    pending = true
    error = ""
    notice = ""
    try {
      const result = await exec(command)
      if (!live(id, path)) return
      requireSuccess(result)
      // A successful command must be followed by a valid fresh revision before another save.
      ready = false
      entryReady = false
      try { parseTargetAck(result.stdout) } catch { throw new Error(m.targetsInvalid) }
      const value = await readStatus(path)
      if (!live(id, path)) return
      accept(value)
      if (ready) notice = m.targetsSaved
    } catch (cause) {
      if (live(id, path)) error = cause instanceof Error ? cause.message : m.targetsFailed
    } finally {
      if (!disposed && sequence === id) pending = false
    }
  }
  function canManageEntry(enabled: boolean): boolean {
    return !disposed && !disabled && !pending && entryReady && !!bin && !!status && !changed() &&
      (!enabled || (!!status.backend?.active && !status.entry.has_own_webui))
  }
  async function setEntry(enabled: boolean) {
    if (!canManageEntry(enabled) || !status) return
    const id = ++sequence, path = bin
    pending = true
    error = ""
    notice = ""
    try {
      const result = await exec(targetEntryCommand(path, enabled))
      if (!live(id, path)) return
      requireSuccess(result)
      ready = false
      entryReady = false
      try { parseTargetAck(result.stdout) } catch { throw new Error(m.targetsInvalid) }
      const value = await readStatus(path)
      if (!live(id, path)) return
      if (value.entry.enabled !== enabled) throw new Error(m.targetsFailed)
      accept(value)
      notice = m.targetsEntryDone
    } catch (cause) {
      if (live(id, path)) {
        ready = false
        error = cause instanceof Error ? cause.message : m.targetsFailed
      }
    } finally {
      if (!disposed && sequence === id) pending = false
    }
  }
</script>



<svelte:window onbeforeunload={beforeUnload} onpointerdown={dismissMenu} onfocusin={dismissMenu} onkeydown={menuKeydown} {onscroll} />

<div class="page-enter target-page relative min-w-0 text-xs" aria-busy={pending}>
  <h1 use:trackTitle class="mb-3 text-xl font-[650] leading-[1.35] tracking-[-.6px] max-[359px]:text-lg">Tricky Store</h1>
  {#if menuOpen}
    <div bind:this={menuElement} id="targets-menu" role="menu" aria-label={m.targetsMore}
      class="target-menu fixed z-50 grid w-64 overflow-y-auto">
      {#each [
        { value: "select", label: m.targetsSelectVisible, icon: CheckCheck, blocked: !editable },
        { value: "clear", label: m.targetsClearVisible, icon: SquareDashed, blocked: !editable },
        { value: "refresh", label: m.refresh, icon: RefreshCw, blocked: locked },
        { value: "system", label: m.targetsSystemApps, icon: ListPlus, blocked: !editable },
        { value: "auto", label: m.targetsAutoAdd, icon: PackagePlus, blocked: !editable },
        { value: "global", label: m.targetsGlobalPolicy, icon: SlidersHorizontal, blocked: !editable },
        { value: "manual", label: m.targetsManual, icon: ListPlus, blocked: !editable },
        { value: "entry", label: m.targetsEntry, icon: Settings2, blocked: !status }
      ] as option}
        {#if ["system", "global", "entry"].includes(option.value)}<div class="target-menu-divider" role="separator"></div>{/if}
        <button type="button" class="target-menu-item"
          role={option.value === "auto" ? "menuitemcheckbox" : "menuitem"} aria-checked={option.value === "auto" ? !!draft?.auto_add_new_apps : undefined}
          disabled={option.blocked} onclick={() => menuAction(option.value)}>
          <option.icon size={16} class="shrink-0" />
          <span class="min-w-0 flex-1">{option.label}</span>
          {#if option.value === "auto"}<span class="menu-check" class:is-checked={!!draft?.auto_add_new_apps} aria-hidden="true"></span>{/if}
        </button>
      {/each}
    </div>
  {/if}
  <div class="grid min-w-0 gap-3">
    {#if status?.backend}
      <div class="{ui.panel} !mb-0 grid gap-2">
        <div class="min-w-0">
          <div class="flex items-center gap-2">
            <span class="min-w-0 font-semibold [overflow-wrap:anywhere]">{status.backend.name || status.backend.module_id || status.backend.identity}</span>
            <span class="shrink-0 rounded-md bg-[var(--inset)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--muted)]">{status.backend.identity}</span>
          </div>
          {#if status.backend.version || status.backend.module_id}
            <p class="mt-0.5 text-[11px] text-[var(--muted)] [overflow-wrap:anywhere]">{status.backend.version ?? ""}{status.backend.version && status.backend.module_id ? " · " : ""}<span class="font-mono">{status.backend.module_id ?? ""}</span></p>
          {/if}
          {#if !status.backend.active}<p class="mt-1 text-[var(--muted)]">{m.backendInactive}</p>{/if}
        </div>
        <a href="#/" class="flex min-h-9 items-center gap-2 rounded-lg bg-[var(--inset)] px-2 py-1.5">
          <KeyRound size={16} /><span class="flex-1">{m.targetsKeybox}</span><ChevronRight size={16} class="text-[var(--muted)]" />
        </a>
      </div>
    {:else if !status && !error}
      <div class="{ui.panel} !mb-0 grid gap-2" aria-hidden="true" data-skeleton="backend">
        <div class="min-w-0">
          <div class="flex items-center gap-2">
            <span class="skeleton-block h-3 w-28"></span>
            <span class="rounded-md bg-[var(--inset)] px-1.5 py-0.5 font-mono text-[10px]"><span class="block h-[1lh] w-6"></span></span>
          </div>
          <p class="mt-0.5 text-[11px]"><span class="skeleton-block h-2.5 w-4/5 max-w-64 align-middle"></span></p>
        </div>
        <div class="flex min-h-9 items-center gap-2 rounded-lg bg-[var(--inset)] px-2 py-1.5">
          <span class="skeleton-block size-4 shrink-0 !bg-[var(--line)]"></span>
          <span class="skeleton-block h-3 w-28 !bg-[var(--line)]"></span>
          <span class="skeleton-block ml-auto size-3 !bg-[var(--line)]"></span>
        </div>
      </div>
    {/if}
    {#if !status && !error}
      <div class="target-search" aria-hidden="true" data-skeleton="search">
        <div class="flex min-h-11 items-center gap-2 rounded-full bg-[var(--panel)] px-3">
          <span class="skeleton-block size-[17px] shrink-0 !rounded-full"></span>
          <span class="skeleton-block h-3 w-32"></span>
          <span class="skeleton-block ml-auto h-2.5 w-12"></span>
        </div>
      </div>
      <div aria-hidden="true" data-skeleton="apps">
        {#each [1, 2, 3, 4, 5, 6, 7, 8] as placeholder}
          <div class="pb-1">
            <div class="flex min-h-16 items-center gap-1 rounded-[10px] bg-[var(--panel)] px-2 py-2">
              <div class="flex min-w-0 flex-1 items-center gap-2">
                <span class="skeleton-block size-9 shrink-0 !rounded-full"></span>
                <span class="grid min-w-0 flex-1 gap-2"><span class="skeleton-block h-3 w-1/2"></span><span class="skeleton-block h-2.5 w-3/4"></span></span>
              </div>
              <span class="size-8 shrink-0"></span>
              <span class="check-hit"><span class="skeleton-block size-[18px] !rounded-[5px]"></span></span>
            </div>
          </div>
        {/each}
      </div>
    {/if}
    {#if error}<p role="alert" class="whitespace-pre-wrap text-[var(--red)] [overflow-wrap:anywhere]">{error}</p>{/if}
    {#if notice && !sheet && !editor}<p role="status" class="target-notice">{notice}</p>{/if}
    {#if status && !status.supported}<p>{m.targetsUnsupported}</p>{/if}
    {#if status?.supported && draft}
      <div class="target-search">
      <div class="flex min-h-11 items-center gap-2 rounded-full bg-[var(--panel)] px-3">
        <Search size={17} class="shrink-0 text-[var(--muted)]" />
        <input class="min-w-0 flex-1 bg-transparent py-2.5 outline-none" type="search" aria-label={m.targetsSearch} placeholder={m.targetsSearch} bind:value={query}
          onkeydown={(event) => { if (event.key === "Escape") query = "" }} />
        <span class="shrink-0 text-[11px] tabular-nums text-[var(--muted)]" aria-live="polite">{m.targetsSelected} {draft.targets.length}</span>
      </div>
      </div>
        <VirtualList items={visible} getKey={(item) => item.package_name} estimateSize={68}>
          {#snippet item(app)}
            <div class="pb-1">
              <AppRow packageName={app.package_name} label={metadata.get(app.package_name)?.label || app.package_name}
                icon={metadata.get(app.package_name)?.icon ?? null} selected={app.selected} mode={app.mode} modeLabel={modeLabel(app.mode)}
                system={app.system} systemLabel={m.targetsSystem} customPolicy={!!draft.per_app_policy[app.package_name]}
                policyLabel={m.targetsAppPolicy} missing={!app.installed} missingLabel={m.targetsMissing} editLabel={m.targetsEdit}
                disabled={!editable} ontoggle={() => select(app.package_name, !app.selected)} ontune={() => openEditor(app.package_name)} />
            </div>
          {/snippet}
        </VirtualList>
      {#if !visible.length}<p class="page-enter py-4 text-center text-[var(--muted)]">{m.targetsEmpty}</p>{/if}
      <div class="target-fab-bar">
        <div class="pointer-events-auto flex justify-end gap-2">
          {#if dirty}
            <button type="button" class="target-fab target-fab-secondary" class:is-collapsed={fabCollapsed}
              disabled={!editable || !!editor || !!sheet} aria-label={m.targetsDiscardAction} onclick={discard}><Undo2 size={18} /><span class="target-fab-label" aria-hidden="true">{m.targetsDiscardAction}</span></button>
          {/if}
          <button type="button" class="target-fab target-fab-primary" class:is-collapsed={fabCollapsed}
            disabled={!editable || !dirty || !validDraft(draft) || !!editor || !!sheet} aria-label={m.targetsSave} onclick={save}>
            <NotebookPen size={18} /><span aria-hidden="true" class="target-fab-label">{m.targetsSave}</span>
            {#if dirty}<span class="target-fab-dot" aria-label={m.targetsUnsaved}></span>{/if}
          </button>
        </div>
      </div>
    {/if}
  </div>
</div>

{#if sheet}
  <Modal title={sheet === "system" ? m.targetsSystemApps : sheet === "global" ? m.targetsGlobalPolicy : sheet === "manual" ? m.targetsManual : m.targetsEntry}
    closeLabel={m.targetsClose} busy={pending} onclose={closeSheet} bind:contentElement={systemScroll}>
    {#if sheet === "system"}
      <div class="target-modal-search">
        <input type="search" class="{inputClass} w-full" aria-label={m.targetsSearch} placeholder={m.targetsSearch} bind:value={systemQuery} />
      </div>
        <VirtualList items={systemRows} getKey={(item) => item.package_name} estimateSize={68}
          windowScroll={false} scrollTarget={systemScroll}>
          {#snippet item(app)}
            <div class="pb-1">
              <AppRow packageName={app.package_name} label={metadata.get(app.package_name)?.label || app.package_name}
                icon={metadata.get(app.package_name)?.icon ?? null} selected={systemDraft.includes(app.package_name)} editLabel={m.targetsEdit}
                disabled={!editable} ontoggle={() => toggleSystem(app.package_name)} />
            </div>
          {/snippet}
        </VirtualList>
      {#if !systemRows.length}<p class="page-enter py-3 text-[var(--muted)]">{m.targetsEmpty}</p>{/if}
    {:else if sheet === "global" && draft}
      <fieldset disabled={!editable} class="grid gap-2">
        {@render policyFields(null, globalPolicy, "global")}
        <p class="text-[11px] leading-relaxed text-[var(--muted)]">{m.targetsPolicyHint}</p>
      </fieldset>
    {:else if sheet === "manual"}
      <fieldset disabled={!editable} class="grid gap-2">
        <input class={inputClass} aria-label={m.targetsManual} placeholder={m.targetsManual} bind:value={manual}
          onkeydown={(event) => { if (event.key === "Enter") { event.preventDefault(); addManual() } }} />
        <select class={inputClass} aria-label={m.targetsMode} bind:value={manualMode}>
          {#each modes as mode}<option value={mode}>{modeLabel(mode)}</option>{/each}
        </select>
        {#if error}<p role="alert" class="text-[var(--red)]">{error}</p>{/if}
      </fieldset>
    {:else if sheet === "entry" && status}
      <label class="flex min-h-10 items-center gap-2 rounded-[10px] bg-[var(--inset)] px-2"><input type="checkbox" class="ui-check" checked={status.entry.enabled}
        disabled={!canManageEntry(!status.entry.enabled)} onchange={(event) => setEntry(event.currentTarget.checked)} />{m.targetsEntry}</label>
      <div role="status" class="mt-2 flex min-h-6 items-center gap-1.5 text-[var(--muted)]">
        {notice}
      </div>
      {#if error}<p role="alert" class="mt-2 whitespace-pre-wrap text-[var(--red)]">{error}</p>{/if}
      {#if status.entry.module_id}<p class="mt-2 font-mono text-[11px] text-[var(--muted)]">{status.entry.module_id}</p>{/if}
      {#if status.entry.linked}<p class="mt-1 text-[var(--muted)]">{m.targetsLinked}</p>{/if}
      {#if status.entry.has_own_webui}<p class="mt-1 text-[var(--muted)]">{m.targetsOwnWebui}</p>{/if}
    {/if}
    {#snippet footer(requestClose)}
      <button type="button" class="{ui.button} min-w-[88px] border border-[var(--line)] bg-[var(--panel)] hover:enabled:bg-[var(--inset)]"
        disabled={pending} onclick={requestClose}>{sheet === "entry" ? m.targetsClose : m.targetsCancel}</button>
      {#if sheet === "system"}
        <button type="button" class="{ui.button} min-w-[88px] bg-[var(--accent)] text-[var(--accent-ink)]"
          disabled={!editable} onclick={() => { applySystem(false); requestClose() }}>{m.targetsApply}</button>
      {:else if sheet === "global"}
        <button type="button" class="{ui.button} min-w-[88px] bg-[var(--accent)] text-[var(--accent-ink)]"
          disabled={!editable || !validPolicy(globalPolicy)} onclick={() => { applyGlobal(false); requestClose() }}>{m.targetsApply}</button>
      {:else if sheet === "manual"}
        <button type="button" class="{ui.button} min-w-[88px] bg-[var(--accent)] text-[var(--accent-ink)]"
          disabled={!editable || !validPackage(manual.trim())} onclick={() => { addManual(); requestClose() }}>{m.targetsAdd}</button>
      {/if}
    {/snippet}
  </Modal>
{/if}

{#if editor}
  <Modal title={m.targetsEdit} description={editor.name} closeLabel={m.targetsClose} busy={pending} onclose={closeEditor}>
    <fieldset disabled={!editable} class="grid gap-1.5">
      <legend class="sr-only">{m.targetsMode}</legend>
      {#each modes as mode}
        <label class="flex min-h-10 cursor-pointer items-center gap-2 rounded-[10px] border border-transparent bg-[var(--inset)] px-2 py-1.5 transition-colors has-[:checked]:border-[var(--accent)] has-[:checked]:bg-[var(--accent-bg)]">
          <input type="radio" name="target-mode" value={mode} bind:group={editor.mode} class="ui-radio" />
          <span>{modeLabel(mode)}</span>
          <span class="ml-auto font-mono text-[11px] text-[var(--muted)]">{mode === "auto" ? "" : mode === "generate" ? "!" : "?"}</span>
        </label>
      {/each}
    </fieldset>
    {#if supportsAppPolicy() || canTrackSystem(editor.name)}
      <details class="mt-3 rounded-[10px] border border-[var(--line)]">
        <summary class="cursor-pointer px-2 py-2 font-medium">{m.targetsSettings}</summary>
        <fieldset disabled={!editable} class="grid gap-2 px-2 pb-2">
          {#if canTrackSystem(editor.name)}
            <label class="flex min-h-8 items-center gap-2"><input type="checkbox" bind:checked={editor.tracked} class="ui-check" />{m.targetsTrackSystem}</label>
          {/if}
          {#if supportsAppPolicy()}
            <p>{m.targetsAppPolicy}</p>
            {@render policyFields(editor.name, editor.policy, "editor")}
            <p class="text-[11px] leading-relaxed text-[var(--muted)]">{m.targetsPolicyHint}</p>
          {/if}
        </fieldset>
      </details>
    {/if}
    {#snippet footer(requestClose)}
      <button type="button" class="{ui.button} min-w-[88px] border border-[var(--line)] bg-[var(--panel)] hover:enabled:bg-[var(--inset)]"
        disabled={pending} onclick={requestClose}>{m.targetsCancel}</button>
      <button type="button" class="{ui.button} min-w-[88px] bg-[var(--accent)] text-[var(--accent-ink)]"
        disabled={!editable || !validPolicy(editor.policy)} onclick={() => { applyEditor(false); requestClose() }}>{m.targetsApply}</button>
    {/snippet}
  </Modal>
{/if}

{#snippet policyFields(name: string | null, policy: Policy, local: "editor" | "global" | null = null)}
  <div class="grid grid-cols-3 gap-1.5 max-[359px]:grid-cols-1">
    {#each policyKeys as key}
      <label class="grid min-w-0 gap-1">
        <span class="font-mono text-[11px] text-[var(--muted)]">{key}</span>
        <input class={inputClass} aria-label={(name ?? m.targetsGlobalPolicy) + " " + key}
          value={policy[key] ?? ""} placeholder={key === "os_patch" ? "YYYYMM / prop / no" : "YYYYMMDD / prop / no"}
          aria-invalid={!validPolicyValue(key, policy[key] ?? "")}
          oninput={(event) => local === "editor" ? editPolicy(key, event.currentTarget.value) : local === "global" ? editGlobalPolicy(key, event.currentTarget.value) : setPolicy(name, key, event.currentTarget.value)} />
      </label>
    {/each}
  </div>
{/snippet}
