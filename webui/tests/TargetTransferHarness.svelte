<script lang="ts">
  import { onMount } from "svelte"
  import { toast } from "../src/lib/bridge"
  import TargetManager from "../src/lib/components/TargetManager.svelte"
  let bin = $state("/module/bin/keybox")
  let disabled = $state(false)
  let visible = $state(true)
  let manager: TargetManager
  onMount(() => {
    const host = window as typeof window & { transferTest?: unknown }
    host.transferTest = {
      context: (value: string) => { bin = value },
      disable: (value: boolean) => { disabled = value },
      unmount: () => { visible = false },
      menu: () => manager.toggleMenu(),
      canLeave: () => manager.canLeave(),
    }
    return () => { delete host.transferTest }
  })
</script>
<main style="padding: var(--page-gutter)">
<div class="target-toolbar" style="position: fixed; top: 0; left: var(--page-edge-left); right: var(--page-edge-right); z-index: 50; margin: 0; background: var(--bg)">
  <button id="targets-menu-trigger" aria-label="More actions" onclick={() => manager.toggleMenu()}>More actions</button>
</div>
{#if visible}<TargetManager bind:this={manager} {bin} {disabled} lang="en" onNotice={toast} />{/if}
</main>
