<script lang="ts">
  import { onMount } from "svelte"
  import LogOutput from "../src/lib/components/LogOutput.svelte"
  import { LogBuffer } from "../src/lib/log"
  let saved = $state(false)
  let loading = $state(false)
  const current = new LogBuffer(8000)
  current.replace("Current output\nLine two")
  const persisted = new LogBuffer(8000)
  persisted.replace(Array.from({ length: 60 }, (_, index) => "Saved log " + index).join("\n"))
  let currentOutput = $state(current.text)
  let currentLines = $state.raw(current.lines.slice())
  let savedOutput = $state(persisted.text)
  let savedLines = $state.raw(persisted.lines.slice())
  let bottomLabel = $state<string | undefined>()
  let mounted = $state(true)

  onMount(() => {
    const host = window as unknown as { logHarness?: {
      appendCurrent: (text: string) => void
      replaceCurrent: (text: string) => void
      appendSaved: (text: string) => void
      setBottomLabel: (label?: string) => void
      setMounted: (value: boolean) => void
    } }
    host.logHarness = {
      appendCurrent(text) {
        current.append(text)
        currentOutput = current.text
        currentLines = current.lines.slice()
      },
      replaceCurrent(text) {
        current.replace(text)
        currentOutput = current.text
        currentLines = current.lines.slice()
      },
      appendSaved(text) {
        persisted.append(text)
        savedOutput = persisted.text
        savedLines = persisted.lines.slice()
      },
      setBottomLabel(label) { bottomLabel = label },
      setMounted(value) { mounted = value },
    }
    return () => { delete host.logHarness }
  })

  async function readSaved() {
    saved = true
    if (loading) return
    loading = true
    const host = window as unknown as { latency?: number; savedReads?: number }
    host.savedReads = (host.savedReads ?? 0) + 1
    await new Promise(resolve => setTimeout(resolve, host.latency ?? 20))
    loading = false
  }
</script>

<div style="max-width: 390px; margin: 12px;">
  {#if mounted}
    <LogOutput output={saved ? savedOutput : currentOutput} lines={saved ? savedLines : currentLines}
      title="Output" hint="No output" currentLabel="Current" savedLabel="Saved"
      loadingLabel="Loading" {bottomLabel} {saved} {loading} onCurrent={() => saved = false} onSaved={readSaved} />
  {/if}
</div>
