<script lang="ts">
  import { onDestroy, untrack } from "svelte"
  import { slide } from "svelte/transition"
  import { Link2, Unlink, Loader2, Search, KeyRound, ChevronRight, ChevronDown, NotebookPen, Undo2, PackagePlus, RefreshCw, ListPlus, SlidersHorizontal, SquareDashed, CheckCheck, Settings2 } from "lucide-svelte"
  import { packagesInfo } from "../apps"
  import { readInstallTimes } from "../installTimes"
  import AppRow from "./AppRow.svelte"
  import AppListSkeleton from "./AppListSkeleton.svelte"
  import Modal from "./Modal.svelte"
  import Dropdown from "./Dropdown.svelte"
  import VirtualList from "./VirtualList.svelte"
  import { exec, type ExecResult } from "../bridge"
  import { messages, type Locale } from "../i18n"
  import { ui } from "../ui"
  import { surface, reveal, pageExit } from "../motion"
  import { stickySearch } from "../stickySearch"
  import {
    modes, policyKeys, validPackage, validDraft, validPolicy, validPolicyValue, parseTargetStatus, parseTargetAck,
    draftFromStatus, targetBackendTitle, targetStatusCommand, targetSaveCommand, targetEntryCommand, visiblePackages,
    parseTargetEntry, targetEntryStatusCommand, type TargetEntry,
    type TargetStatus, type TargetDraft, type TargetMode, type PolicyKey, type Policy, type TargetSort,
  } from "../targets"

  let { bin, lang, disabled = false, onMenuChange, onTitleVisibility, onTitleChange }: { bin: string; lang: Locale; disabled?: boolean; onMenuChange?: (open: boolean) => void; onTitleVisibility?: (visible: boolean) => void; onTitleChange?: (title: string) => void } = $props()
  let m = $derived(messages[lang])
  let status = $state<TargetStatus | null>(null)
  let draft = $state<TargetDraft | null>(null)
  let baseline = $state("")
  let initialSelection = $state<ReadonlySet<string> | null>(null)
  let ready = $state(false)
  let entryReady = $state(false)
  let pending = $state(false)
  let entry = $state<TargetEntry | null>(null)
  let entryBusy = $state(false)
  let entryError = $state("")
  let entryNotice = $state("")
  let entrySequence = 0
  let saving = $state(false)
  let error = $state("")
  let notice = $state("")
  let query = $state("")
  let filter = $state("listed")
  const preferenceKey = "keybox-lite.target-list-preferences"
  const storedPreferences = (() => { try { const value = JSON.parse(localStorage.getItem(preferenceKey) ?? "{}"); return value && typeof value === "object" ? value : {} } catch { return {} } })()
  const preferredSort: TargetSort = ["name", "package", "installed"].includes(storedPreferences.sort) ? storedPreferences.sort : "name"
  let requestedSort = $state<TargetSort>(preferredSort)
  let targetSort = $state<TargetSort>(preferredSort === "installed" ? "name" : preferredSort)
  let sortReady = $state(preferredSort !== "installed")
  let showSystemApps = $state(storedPreferences.showSystem === true)
  let sortMenuOpen = $state(false)
  let installTimes = $state<Map<string, number>>(new Map())
  let installTimesLoading = $state(false)
  let installTimesError = $state("")
  let installTimesSequence = 0
  let installTimesController: AbortController | undefined
  let checkedInstallNames = ""
  function saveListPreferences() { try { localStorage.setItem(preferenceKey, JSON.stringify({ sort: sortReady ? targetSort : requestedSort, showSystem: showSystemApps })) } catch {} }
  function cancelInstallSort() {
    installTimesSequence++
    installTimesController?.abort()
    installTimesController = undefined
    installTimesLoading = false
  }
  async function setSort(value: TargetSort) {
    if (disposed || disabled || !bin) return
    if (value === "installed" && installTimesLoading && requestedSort === value) return
    cancelInstallSort()
    requestedSort = value
    installTimesError = ""
    if (value !== "installed") {
      targetSort = value
      sortReady = true
      saveListPreferences()
      return
    }
    if (!status) { sortReady = false; return }
    const id = installTimesSequence, path = bin, names = metadataNames
    const controller = new AbortController()
    installTimesController = controller
    installTimesLoading = true
    const live = () => !disposed && !disabled && bin === path && id === installTimesSequence && !controller.signal.aborted
    try {
      const hasPackages = status.packages.some(item => !item.package_name.startsWith("uid:")) ||
        !!draft?.targets.some(item => !item.package_name.startsWith("uid:"))
      const times = hasPackages ? await readInstallTimes(controller.signal) : new Map<string, number>()
      if (!live()) return
      if (hasPackages && !times.size) throw new Error("Missing install times")
      // Publish the data and selected sort together; the previous list remains
      // visible while the request is pending.
      installTimes = times
      checkedInstallNames = names
      targetSort = "installed"
      sortReady = true
      saveListPreferences()
    } catch {
      if (live()) {
        checkedInstallNames = names
        requestedSort = targetSort
        sortReady = true
        installTimesError = m.targetsInstallTimeFailed
      }
    } finally {
      if (id === installTimesSequence) { installTimesLoading = false; installTimesController = undefined }
    }
  }
  function toggleSystemVisibility() { showSystemApps = !showSystemApps; saveListPreferences() }
  let manual = $state("")
  let manualMode = $state<TargetMode>("auto")
  let manualModeOpen = $state(false)
  let editor = $state<{ name: string; mode: TargetMode; policy: Policy; tracked: boolean } | null>(null)
  let editorBaseline = ""
  let advancedOpen = $state(false)
  const componentId = $props.id()
  const advancedId = componentId + "-advanced"
  let menuOpen = $state(false)
  let menuElement = $state<HTMLDivElement>()
  let sheet = $state<"system" | "global" | "manual" | "entry" | null>(null)
  let systemQuery = $state("")
  let systemScroll = $state<HTMLDivElement>()
  let globalPolicy = $state<Policy>({})
  let globalBaseline = ""
  let systemDraft = $state<string[]>([])
  let systemBaseline = ""
  let metadata = $state<Map<string, { label: string; icon: string | null }>>(new Map())
  let metadataSequence = 0
  let metadataNames = $derived(status ? [...new Set([...status.packages.map(item => item.package_name), ...(draft?.targets.map(item => item.package_name) ?? [])])].sort().join("\n") : "")
  let systemSelection = $state<ReadonlySet<string>>(new Set())
  let systemRows = $derived(status && draft ? visiblePackages(status, draft, systemQuery, "system", metadata, systemSelection) : [])
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
  let sortMetadata = $derived(new Map(metadataNames.split("\n").filter(Boolean).map(name => [name, { label: metadata.get(name)?.label || name, installedAt: installTimes.get(name.includes("@") ? name.split("@")[0] + "@" + Number(name.split("@")[1]) : name) }])))
  let visible = $derived(status && draft ? visiblePackages(status, draft, query, filter, sortMetadata, initialSelection ?? undefined, targetSort, showSystemApps) : [])
  const inputClass = "min-h-8 min-w-0 rounded-lg border border-[var(--line)] bg-[var(--inset)] px-2 py-1 text-xs text-[var(--text)] disabled:opacity-50"

  $effect(() => {
    if (bin !== contextBin) {
      cancelInstallSort()
      if (sortReady) requestedSort = targetSort
      installTimes = new Map()
      checkedInstallNames = ""
      sortReady = requestedSort !== "installed"
      targetSort = requestedSort === "installed" ? "name" : requestedSort
      closeEditor()
      closeSheet()
      menuOpen = false
      contextBin = bin
      sequence++
      pending = false
      ready = false
      entryReady = false
      status = null
      initialSelection = null
      entrySequence++
      entry = null
      entryBusy = false
      entryError = ""
      entryNotice = ""
      saving = false
      metadata = new Map()
      draft = null
      baseline = ""
      error = ""
      notice = ""
    }
    if (disabled) { closeEditor(); closeSheet(); menuOpen = false }
    if (disabled && installTimesLoading) {
      cancelInstallSort()
      if (sortReady) requestedSort = targetSort
    }
    if (disabled && entryBusy) { entrySequence++; entryBusy = false; entryReady = false }
    if (disabled && pending) {
      saving = false
      sequence++
      pending = false
      ready = false
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
  onDestroy(() => { disposed = true; cancelInstallSort(); sequence++; entrySequence++; metadataSequence++ })

  function loadMetadata(names: string[], path: string) {
    const id = ++metadataSequence
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), 6000)
    const apply = (value: Map<string, { label: string; icon: string | null }>) => {
      if (!disposed && !controller.signal.aborted && id === metadataSequence && bin === path && !disabled) metadata = value
    }
    void packagesInfo(names, controller.signal, apply).then(apply).catch(() => {
      // Keep any batches already received when a later batch fails.
    }).finally(() => {
      clearTimeout(timer)
    })
    return () => { controller.abort(); clearTimeout(timer); if (id === metadataSequence) metadataSequence++ }
  }

  $effect(() => {
    const path = bin, blocked = disabled, initial = !sortReady, loaded = ready
    const names = metadataNames, sort = targetSort, loading = installTimesLoading
    untrack(() => {
      if (path && !blocked && loaded && !loading && (initial || (sort === "installed" && names !== checkedInstallNames))) {
        void setSort(initial ? requestedSort : sort)
      }
    })
  })
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
  export function toggleMenu() { sortMenuOpen = false; menuOpen = !menuOpen }
  function closeMenu(focus = false) {
    menuOpen = false
    sortMenuOpen = false
    if (focus) document.getElementById("targets-menu-trigger")?.focus()
  }
  function dismissMenu(event: Event) {
    const target = event.target
    if (!(target instanceof Element) || target.closest("#targets-menu-trigger")) return
    if (menuOpen && !menuElement?.contains(target)) closeMenu()
  }
  function menuKeydown(event: KeyboardEvent) {
    if (!menuOpen || event.defaultPrevented) return
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
    else if (action === "clear") selectVisible(false)
    else if (action === "auto") autoAdd(!draft?.auto_add_new_apps)
    else if (["system", "global", "manual", "entry"].includes(action)) openSheet(action as NonNullable<typeof sheet>)
  }
  function openSheet(value: NonNullable<typeof sheet>) {
    if (disposed || disabled || !bin) return
    if (value === "system" && (!canEdit() || !status || !draft)) return
    if (value === "system" && draft && status) {
      systemQuery = ""
      systemDraft = [...draft.system_apps]
      systemSelection = new Set(systemDraft)
      systemBaseline = JSON.stringify(systemDraft)
    }
    if (value === "global" && draft) {
      globalPolicy = { ...draft.default_policy }
      globalBaseline = JSON.stringify(globalPolicy)
    }
    sheet = value
    if (value === "entry") void refreshEntry()
  }
  function closeSheet() { manualModeOpen = false; sheet = null }
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
    initialSelection = null
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
    if (entryBusy) { entryNotice = m.targetsPending; return false }
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
    advancedOpen = false
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
    if (changed() || editorChanged() || sheetChanged() || pending || entryBusy) { event.preventDefault(); event.returnValue = "" }
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
  function accept(value: TargetStatus, entryVersion?: number) {
    status = value
    onTitleChange?.(targetBackendTitle(value.backend, m.targetsTitle))
    if (entryVersion === entrySequence && !entryBusy) {
      entry = value.entry
      entryReady = true
      entryError = ""
      entryNotice = ""
    }
    ready = !value.config_error && !value.packages_error
    if (!ready) {
      error = [value.config_error, value.packages_error].filter(Boolean).join("\n")
      return
    }
    if (initialSelection === null) initialSelection = new Set(value.config.targets.map(item => item.package_name))
    draft = draftFromStatus(value)
    if (value.backend?.backend !== "tricky-store") draft.per_app_policy = {}
    baseline = JSON.stringify(draft)
  }
  async function refresh(ask = true, quiet = false) {
    if (disabled || pending || !bin || disposed) return
    if (ask && (changed() || editorChanged() || sheetChanged()) && !window.confirm(m.targetsDiscard)) return
    const resetOrder = changed()
    closeEditor()
    if (sheet !== "entry") closeSheet()
    const id = ++sequence, path = bin, entryVersion = entrySequence
    pending = true
    ready = false
    error = ""
    notice = ""
    try {
      const value = await readStatus(path)
      if (!live(id, path)) return
      if (resetOrder) initialSelection = null
      accept(value, entryVersion)
      if (ready && !quiet) notice = m.targetsRefreshed
    } catch (cause) {
      if (live(id, path)) error = cause instanceof Error ? cause.message : m.targetsFailed
    } finally {
      if (!disposed && sequence === id) { pending = false; saving = false }
    }
  }
  function canEdit(): boolean {
    return !disposed && !disabled && !pending && ready && !!bin && !!status?.supported && !!status?.backend?.active && !!draft
  }
  function select(name: string, enabled: boolean) {
    if (!canEdit() || !draft) return
    if (canTrackSystem(name) && !draft.system_apps.includes(name)) draft.system_apps.push(name)
    if (enabled) {
      if (!draft.targets.some(item => item.package_name === name)) {
        draft.targets.push({ package_name: name, mode: status?.packages.find(item => item.package_name === name)?.mode ?? "auto" })
      }
    } else {
      draft.targets = draft.targets.filter(item => item.package_name !== name)
      delete draft.per_app_policy[name]
    }
    notice = ""
  }
  function selectVisible(enabled: boolean) {
    if (!status || !draft) return
    const scope = visiblePackages(status, draft, query, filter, sortMetadata, initialSelection ?? undefined, targetSort, showSystemApps)
    const selected = new Map(draft.targets.map(item => [item.package_name, item]))
    const tracked = new Set(draft.system_apps)
    for (const item of scope) {
      if (item.system && !item.package_name.startsWith("uid:")) tracked.add(item.package_name)
      if (enabled) {
        if (!selected.has(item.package_name)) {
          selected.set(item.package_name, { package_name: item.package_name, mode: item.mode })
        }
      } else {
        selected.delete(item.package_name)
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
  // Only Tricky Store's formats carry per-app modes and patch policies; TEESimulator
  // and OhMyKeymint store a plain package list.
  const modeBackends = new Set(["tricky-store", "tricky-store-legacy", "tee-simulator-rs"])
  function supportsModes(): boolean { return modeBackends.has(status?.backend?.backend ?? "") }
  function supportsGlobalPolicy(): boolean { return supportsModes() }
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
    if (!canEdit() || entryBusy || !status || !draft || editor || sheet || !changed()) return
    if (!validDraft(draft)) { error = m.targetsInvalid; return }
    const id = ++sequence, path = bin, entryVersion = entrySequence
    const command = targetSaveCommand(path, status.revision, { ...draft, per_app_policy: supportsAppPolicy() ? draft.per_app_policy : {} })
    pending = true
    saving = true
    error = ""
    notice = ""
    try {
      const result = await exec(command)
      if (!live(id, path)) return
      requireSuccess(result)
      // A successful command must be followed by a valid fresh revision before another save.
      ready = false
      try { parseTargetAck(result.stdout) } catch { throw new Error(m.targetsInvalid) }
      const value = await readStatus(path)
      if (!live(id, path)) return
      accept(value, entryVersion)
      if (ready) notice = m.targetsSaved
    } catch (cause) {
      if (live(id, path)) error = cause instanceof Error ? cause.message : m.targetsFailed
    } finally {
      if (!disposed && sequence === id) { pending = false; saving = false }
    }
  }
  function entryLive(id: number, path: string): boolean {
    return !disposed && entrySequence === id && bin === path && !disabled
  }
  async function readEntry(path: string): Promise<TargetEntry> {
    const result = await exec(targetEntryStatusCommand(path))
    requireSuccess(result)
    try { return parseTargetEntry(result.stdout) } catch { throw new Error(m.targetsInvalid) }
  }
  async function refreshEntry() {
    if (disposed || disabled || entryBusy || !bin) return
    const id = ++entrySequence, path = bin
    entryBusy = true
    entryReady = false
    entryError = ""
    entryNotice = ""
    try {
      const value = await readEntry(path)
      if (!entryLive(id, path)) return
      entry = value
      entryReady = true
    } catch (cause) {
      if (entryLive(id, path)) entryError = cause instanceof Error ? cause.message : m.targetsFailed
    } finally {
      if (!disposed && entrySequence === id) { entryBusy = false; entryNotice = "" }
    }
  }
  function canManageEntry(enabled: boolean): boolean {
    return !disposed && !disabled && !saving && !entryBusy && !changed() && entryReady && !!bin && !!entry &&
      (!enabled || (!!status?.backend?.active && !!status.backend.module_id &&
        status.backend.module_id === entry.module_id && !entry.has_own_webui))
  }
  function entryChecked(): boolean { return !!entry && (entry.enabled || entry.linked) }
  function entryStateLabel(): string {
    if (!entryReady && !entryBusy) return m.targetsEntryUnknown
    if (!entry && entryBusy) return m.loading
    if (entry?.linked) return m.targetsEntryOn
    return entry?.enabled ? m.targetsEntryWaiting : m.targetsEntryOff
  }
  async function setEntry(enabled: boolean) {
    if (!canManageEntry(enabled)) return
    const id = ++entrySequence, path = bin
    entryBusy = true
    entryError = ""
    entryNotice = ""
    entryReady = false
    try {
      const result = await exec(targetEntryCommand(path, enabled))
      if (!entryLive(id, path)) return
      // A failed command may have removed links before persistence failed.
      const value = await readEntry(path)
      if (!entryLive(id, path)) return
      entry = value
      entryReady = true
      requireSuccess(result)
      try { parseTargetAck(result.stdout) } catch { throw new Error(m.targetsInvalid) }
      if (value.enabled !== enabled || value.linked !== enabled) throw new Error(m.targetsFailed)
      entryNotice = m.targetsEntryDone
    } catch (cause) {
      if (entryLive(id, path)) {
        entryNotice = ""
        entryError = cause instanceof Error ? cause.message : m.targetsFailed
      }
    } finally {
      if (!disposed && entrySequence === id) entryBusy = false
    }
  }
</script>



<svelte:window onbeforeunload={beforeUnload} onpointerdown={dismissMenu} onfocusin={dismissMenu} onkeydown={menuKeydown} {onscroll} />

<div class="page-enter target-page relative min-w-0 text-xs" aria-busy={pending} out:pageExit|global>
  <h1 use:trackTitle class="mb-3 text-xl font-[650] leading-[1.35] tracking-[-.6px] max-[359px]:text-lg">{targetBackendTitle(status?.backend, m.targetsTitle)}</h1>
  {#if menuOpen}
    <div bind:this={menuElement} id="targets-menu" role="menu" aria-label={m.targetsMore}
      class="target-menu fixed z-50 grid w-64 overflow-y-auto" transition:surface>
      {#each [
        { value: "select", label: m.targetsSelectVisible, icon: CheckCheck, blocked: !editable },
        { value: "clear", label: m.targetsClearVisible, icon: SquareDashed, blocked: !editable },
        { value: "refresh", label: m.refresh, icon: RefreshCw, blocked: locked },
        { value: "manual", label: m.targetsManual, icon: ListPlus, blocked: !editable },
        { value: "system", label: m.targetsSystemApps, icon: ListPlus, blocked: !editable },
        { value: "auto", label: m.targetsAutoAdd, icon: PackagePlus, blocked: !editable },
        { value: "global", label: m.targetsGlobalPolicy, icon: SlidersHorizontal, blocked: !editable || !supportsGlobalPolicy() },
        { value: "entry", label: m.targetsEntry, icon: Settings2, blocked: disabled || !bin }
      ] as option}
        {#if ["manual", "global"].includes(option.value)}<div class="target-menu-divider" role="separator"></div>{/if}
        <button type="button" class="target-menu-item"
          role={option.value === "auto" ? "menuitemcheckbox" : "menuitem"} aria-checked={option.value === "auto" ? !!draft?.auto_add_new_apps : undefined}
          disabled={option.blocked} onclick={() => menuAction(option.value)}>
          <option.icon size={16} class="shrink-0" />
          <span class="min-w-0 flex-1">{option.label}</span>
          {#if option.value === "auto"}<span class="menu-check" class:is-checked={!!draft?.auto_add_new_apps} aria-hidden="true"></span>{/if}
        </button>
        {#if option.value === "refresh"}
          <div class="target-menu-divider" role="separator"></div>
          <div class="grid gap-0.5" role="group" aria-label={m.targetsSort}>
            <Dropdown id={componentId + "-list-sort"} label={m.targetsSort}
              options={[{ value: "name", label: m.targetsSortName }, { value: "package", label: m.targetsSortPackage }, { value: "installed", label: m.targetsSortInstalled }]}
              selected={targetSort} open={sortMenuOpen} disabled={disabled || !ready || !sortReady} triggerClass="target-menu-item w-full"
              onOpenChange={(open) => sortMenuOpen = open} onSelect={setSort}>
              {#snippet trigger()}<SlidersHorizontal size={16} class="shrink-0" /><span class="min-w-0 flex-1">{m.targetsSort}</span><span class="shrink-0 text-[11px] text-[var(--muted)]">{!sortReady ? m.loading : targetSort === "name" ? "A–Z" : targetSort === "package" ? m.targetsSortPackage : m.targetsSortInstalled}</span><ChevronRight size={14} />{/snippet}
            </Dropdown>
            <button type="button" class="target-menu-item w-full" role="menuitemcheckbox" aria-checked={showSystemApps}
              onclick={toggleSystemVisibility}><ListPlus size={16} class="shrink-0" /><span class="min-w-0 flex-1">{m.targetsShowSystem}</span><span class="menu-check" class:is-checked={showSystemApps} aria-hidden="true"></span></button>
          </div>
        {/if}
      {/each}
    </div>
  {/if}
  <div class="grid min-w-0 gap-3">
    {#if status?.backend}
      <div class="{ui.panel} !mb-0 grid gap-2">
        <div class="min-w-0">
          <div class="flex items-center gap-2">
            <span class="min-w-0 font-semibold [overflow-wrap:anywhere]">{targetBackendTitle(status.backend, m.targetsTitle)}</span>
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
      <div class="target-search" use:stickySearch aria-hidden="true" data-skeleton="search">
        <div class="flex min-h-11 items-center gap-2 rounded-full bg-[var(--panel)] px-3">
          <span class="skeleton-block size-[17px] shrink-0 !rounded-full"></span>
          <span class="skeleton-block h-3 w-32"></span>
          <span class="skeleton-block ml-auto h-2.5 w-12"></span>
        </div>
      </div>
      <AppListSkeleton />
    {/if}
    {#if error}<p role="alert" class="whitespace-pre-wrap text-[var(--red)] [overflow-wrap:anywhere]">{error}</p>{/if}
    {#if installTimesLoading}<p role="status" class="target-notice">{m.loading}</p>{/if}
    {#if installTimesError}<p role="alert" class="target-notice">{installTimesError}</p>{/if}
    {#if notice && !sheet && !editor}<p role="status" class="target-notice" transition:surface>{notice}</p>{/if}
    {#if status && !status.supported}<p>{m.targetsUnsupported}</p>{/if}
    {#if status?.supported && draft}
      <div class="target-search" use:stickySearch>
      <div class="flex min-h-11 items-center gap-2 rounded-full bg-[var(--panel)] px-3">
        <Search size={17} class="shrink-0 text-[var(--muted)]" />
        <input class="min-w-0 flex-1 bg-transparent py-2.5 outline-none" type="search" aria-label={m.targetsSearch} placeholder={m.targetsSearch} bind:value={query}
          onkeydown={(event) => { if (event.key === "Escape") query = "" }} />
        <span class="shrink-0 text-[11px] tabular-nums text-[var(--muted)]" aria-live="polite">{m.targetsSelected} {draft.targets.length}</span>
      </div>
      </div>
      {#if ready && sortReady}
        <VirtualList items={visible} getKey={(item) => item.package_name} estimateSize={68}>
          {#snippet item(app)}
            <div class="pb-1">
              <AppRow packageName={app.package_name} label={metadata.get(app.package_name)?.label || app.package_name}
                icon={metadata.get(app.package_name)?.icon ?? null} selected={app.selected} mode={app.mode} modeLabel={modeLabel(app.mode)}
                system={app.system} systemLabel={m.targetsSystem} customPolicy={!!draft?.per_app_policy[app.package_name]}
                policyLabel={m.targetsAppPolicy} missing={!app.installed} missingLabel={m.targetsMissing} editLabel={m.targetsEdit}
                disabled={!editable} ontoggle={() => select(app.package_name, !app.selected)}
                ontune={supportsModes() || supportsAppPolicy() ? () => openEditor(app.package_name) : undefined} />
            </div>
          {/snippet}
        </VirtualList>
      {:else}
        <AppListSkeleton />
      {/if}
      {#if ready && sortReady && !visible.length}<p class="page-enter py-4 text-center text-[var(--muted)]">{m.targetsEmpty}</p>{/if}
      <div class="target-fab-bar">
        <div class="pointer-events-auto flex items-end justify-end">
          {#if dirty}
            <div class="target-fab-item" transition:reveal>
            <button type="button" class="target-fab target-fab-secondary" class:is-collapsed={fabCollapsed}
              disabled={!editable || !!editor || !!sheet} aria-label={m.targetsDiscardAction} onclick={discard}><Undo2 size={18} /><span class="target-fab-label" aria-hidden="true">{m.targetsDiscardAction}</span></button>
            </div>
          {/if}
          <button type="button" class="target-fab target-fab-primary" class:is-collapsed={fabCollapsed} transition:surface={{ y: 6, scale: .96 }}
            disabled={!editable || !dirty || !validDraft(draft) || !!editor || !!sheet} aria-label={m.targetsSave} onclick={save}>
            <NotebookPen size={18} /><span aria-hidden="true" class="target-fab-label">{m.targetsSave}</span>
            {#if dirty}<span class="target-fab-dot" aria-label={m.targetsUnsaved} transition:surface={{ y: 0, scale: .5, duration: 140 }}></span>{/if}
          </button>
        </div>
      </div>
    {/if}
  </div>
</div>

{#if sheet}
  <Modal title={sheet === "system" ? m.targetsSystemApps : sheet === "global" ? m.targetsGlobalPolicy : sheet === "manual" ? m.targetsManual : m.targetsEntry}
    closeLabel={m.targetsClose} busy={sheet === "entry" ? entryBusy : pending} hideFooter={sheet === "entry"} onclose={closeSheet} bind:contentElement={systemScroll}>
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
        {#if supportsModes()}
            <Dropdown id={componentId + "-manual-mode"} label={m.targetsMode}
              options={modes.map(value => ({ value, label: modeLabel(value) }))} selected={manualMode}
              open={manualModeOpen} disabled={!editable} triggerClass={ui.selectButton + " w-full"}
              onOpenChange={(open) => manualModeOpen = open} onSelect={(value) => manualMode = value} />
        {/if}
        {#if error}<p role="alert" class="text-[var(--red)]">{error}</p>{/if}
      </fieldset>
    {:else if sheet === "entry"}
      <p class="text-[11px] leading-relaxed text-[var(--muted)]">{m.targetsEntryHint}</p>
      <div class="entry-controls mt-2">
        <div class="min-w-0">
          <p class="text-xs font-medium [overflow-wrap:anywhere]">{targetBackendTitle(status?.backend, m.targetsEntry)}</p>
          <div id={componentId + "-entry-status"} role="status" aria-live="polite" class="entry-status">
            {#key entryStateLabel()}<span in:surface={{ y: 0, scale: 1, duration: 160 }}>{entryStateLabel()}</span>{/key}
          </div>
        </div>
        <button type="button" class="entry-action" class:is-remove={entryReady && entryChecked()}
          aria-describedby={componentId + "-entry-status"} aria-busy={entryBusy}
          disabled={entryBusy || (!entryReady ? disabled || !bin : !canManageEntry(!entryChecked()))}
          onclick={() => !entryReady ? refreshEntry() : setEntry(!entryChecked())}>
          <span class="entry-action-icon" aria-hidden="true">
            {#if entryBusy}<Loader2 size={14} class="motion-safe:animate-spin" />
            {:else}<span class="grid" in:surface={{ y: 0, scale: .9, duration: 120 }}>
              {#if !entryReady}<RefreshCw size={14} />{:else if entryChecked()}<Unlink size={14} />{:else}<Link2 size={14} />{/if}
            </span>{/if}
          </span>
          <span class="entry-action-label">
            {#each [m.targetsEntryInject, m.targetsEntryRemove, m.refresh, m.loading] as label}
              <span class:invisible={label !== (entryBusy ? m.loading : !entryReady ? m.refresh : entryChecked() ? m.targetsEntryRemove : m.targetsEntryInject)}
                aria-hidden={label !== (entryBusy ? m.loading : !entryReady ? m.refresh : entryChecked() ? m.targetsEntryRemove : m.targetsEntryInject)}>{label}</span>
            {/each}
          </span>
        </button>
      </div>
      {#if dirty}<p class="mt-1.5 text-[11px] text-[var(--muted)]">{m.targetsEntrySaveFirst}</p>{/if}
      {#if entryError}<p role="alert" class="mt-2 whitespace-pre-wrap text-[var(--red)]" transition:slide={{ duration: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : 160 }}>{entryError}</p>{/if}
      {#if entry?.has_own_webui}<p class="mt-1 text-[var(--muted)]">{m.targetsOwnWebui}</p>{/if}
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
    {#if supportsModes()}
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
    {/if}
    {#if supportsAppPolicy() || canTrackSystem(editor.name)}
      <div class="mt-3 rounded-[10px] border border-[var(--line)]">
        <button type="button" class="flex min-h-10 w-full items-center justify-between gap-2 px-2 py-2 text-left font-medium"
          aria-expanded={advancedOpen} aria-controls={advancedId} onclick={() => advancedOpen = !advancedOpen}>
          {m.targetsSettings}<ChevronDown size={16} class={"text-[var(--muted)] transition-transform duration-200 " + (advancedOpen ? "rotate-180" : "")} />
        </button>
        <div id={advancedId} class={"grid transition-[grid-template-rows,opacity] duration-200 " + (advancedOpen ? "grid-rows-[1fr] opacity-100" : "grid-rows-[0fr] opacity-0")} inert={!advancedOpen}>
        <div class="min-h-0 overflow-hidden">
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
        </div>
        </div>
      </div>
    {/if}
    {#snippet footer(requestClose)}
      <button type="button" class="{ui.button} min-w-[88px] border border-[var(--line)] bg-[var(--panel)] hover:enabled:bg-[var(--inset)]"
        disabled={pending} onclick={requestClose}>{m.targetsCancel}</button>
      <button type="button" class="{ui.button} min-w-[88px] bg-[var(--accent)] text-[var(--accent-ink)]"
        disabled={!editable || !editor || !validPolicy(editor.policy)} onclick={() => { applyEditor(false); requestClose() }}>{m.targetsApply}</button>
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
<style>
  .entry-controls { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
  .entry-status { margin-top: 2px; color: var(--muted); font-size: 11px; overflow-wrap: anywhere; }
  .entry-action { position: relative; flex-shrink: 0; min-height: 32px; padding: 4px 10px 4px 24px; border: 1px solid var(--line); border-radius: 6px; background: var(--accent-bg); color: var(--accent); font-size: 12px; transition: background-color 160ms, color 160ms, opacity 160ms, transform 160ms; }
  .entry-action.is-remove { background: var(--inset); color: var(--text); }
  .entry-action:disabled:not([aria-busy="true"]) { opacity: .5; }
  .entry-action:active:not(:disabled) { transform: scale(.98); }
  .entry-action:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .entry-action-icon { position: absolute; left: 6px; top: 50%; display: grid; width: 14px; height: 14px; place-items: center; transform: translateY(-50%); }
  .entry-action-label { display: grid; }
  .entry-action-label > span { grid-area: 1 / 1; }
  @media (max-width: 359px) { .entry-controls { gap: 6px; } }
</style>
