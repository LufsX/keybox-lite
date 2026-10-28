<script lang="ts">
  import LogOutput from "../src/lib/components/LogOutput.svelte"
  import { LogBuffer } from "../src/lib/log"
  let saved = $state(false)
  let loading = $state(false)
  const current = new LogBuffer(8000)
  current.replace("Current output\nLine two")
  const persisted = new LogBuffer(8000)
  persisted.replace(Array.from({ length: 60 }, (_, index) => "Saved log " + index).join("\n"))
  async function readSaved() {
    loading = true
    await new Promise(resolve => setTimeout(resolve, (window as unknown as { latency?: number }).latency ?? 20))
    saved = true
    loading = false
  }
</script>

<div style="max-width: 390px; margin: 12px;">
  <LogOutput output={saved ? persisted.text : current.text} lines={saved ? persisted.lines : current.lines}
    title="Output" hint="No output" currentLabel="Current" savedLabel="Saved"
    loadingLabel="Loading" {saved} {loading} onCurrent={() => saved = false} onSaved={readSaved} />
</div>
