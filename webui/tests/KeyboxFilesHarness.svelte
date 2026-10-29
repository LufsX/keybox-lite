<script lang="ts">
  import { onMount } from "svelte"
  import KeyboxFiles from "../src/lib/components/KeyboxFiles.svelte"
  import type { Locale } from "../src/lib/i18n"
  let bin = $state("/module/bin/keybox")
  let moddir = $state("/module")
  let disabled = $state(false)
  let running = $state<"install" | "restore" | null>(null)
  let mounted = $state(true)
  let lang = $state<Locale>("en")
  let saved = $state(false)

  onMount(() => {
    const host = window as unknown as { filesHarness?: unknown; applyKeybox?: (operation: string, id: string, revision: string) => Promise<void> }
    host.filesHarness = {
      configure(value: { bin?: string; moddir?: string; disabled?: boolean; mounted?: boolean; lang?: Locale; running?: "install" | "restore" | null }) {
        if (value.bin !== undefined) bin = value.bin
        if (value.moddir !== undefined) moddir = value.moddir
        if (value.disabled !== undefined) disabled = value.disabled
        if (value.mounted !== undefined) mounted = value.mounted
        if (value.lang !== undefined) lang = value.lang
        if (value.running !== undefined) running = value.running
      },
    }
    return () => { delete host.filesHarness }
  })
  async function onInstall(operation: "install" | "restore", id: string, revision: string) {
    disabled = true
    running = operation
    saved = false
    try {
      await (window as unknown as { applyKeybox: (operation: string, id: string, revision: string) => Promise<void> }).applyKeybox(operation, id, revision)
    } finally { disabled = false; running = null }
  }
</script>

<div style="max-width: 400px; margin: 12px;">
  {#if mounted}<KeyboxFiles {bin} {moddir} {lang} {disabled} {running} {onInstall} />{/if}
  <div aria-label="Output navigation">
    <button onclick={() => saved = false}>Current output</button>
    <button onclick={() => saved = true}>Saved log</button>
    <p role="log">{saved ? "Saved output" : "Current output"}</p>
  </div>
</div>
