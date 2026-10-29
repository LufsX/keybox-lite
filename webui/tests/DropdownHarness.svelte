<script lang="ts">
  import Dropdown from "../src/lib/components/Dropdown.svelte"
  import Modal from "../src/lib/components/Modal.svelte"
  let { standalone = false, multiple = false }: { standalone?: boolean; multiple?: boolean } = $props()
  let visible = $state(true)
  let mounted = $state(true)
  let open = $state(false)
  let secondaryOpen = $state(false)
  let disabled = $state(false)
  let reducedMotion = $state(false)
  let selected = $state("two")
  const options = Array.from({ length: 12 }, (_, i) => ({ value: i === 1 ? "two" : String(i), label: "Option " + i }))

  export function configure(state: { open?: boolean; secondaryOpen?: boolean; disabled?: boolean; reducedMotion?: boolean; mounted?: boolean; visible?: boolean }) {
    if (state.open !== undefined) open = state.open
    if (state.secondaryOpen !== undefined) secondaryOpen = state.secondaryOpen
    if (state.disabled !== undefined) disabled = state.disabled
    if (state.reducedMotion !== undefined) reducedMotion = state.reducedMotion
    if (state.mounted !== undefined) mounted = state.mounted
    if (state.visible !== undefined) visible = state.visible
  }
</script>

{#snippet dropdowns()}
  {#if mounted}
    <Dropdown id="test-options" label="Mode" {options} {selected} {open} {disabled} {reducedMotion}
      triggerClass="test-trigger" onOpenChange={value => open = value} onSelect={value => selected = value} />
    {#if multiple}
      <Dropdown id="secondary-options" label="Other mode" {options} {selected} open={secondaryOpen}
        triggerClass="test-trigger" onOpenChange={value => secondaryOpen = value} onSelect={value => selected = value} />
    {/if}
  {/if}
{/snippet}

{#if visible}
  {#if standalone}
    <div class="app-shell">
      <section class="glass-panel" style="margin: 100px 24px 0; padding: 12px; border-radius: 10px;">
        {@render dropdowns()}
      </section>
      <button type="button" data-outside style="position: fixed; z-index: 50; inset: 180px 0 0; background: var(--inset);">Adjacent surface</button>
    </div>
  {:else}
    <Modal title="Dropdown test" onclose={() => visible = false}>
      <div style="height: 160px">Scrollable content</div>
      {@render dropdowns()}
      <button type="button" data-outside>Outside menu</button>
      <div style="height: 160px">More content</div>
    </Modal>
  {/if}
{/if}
