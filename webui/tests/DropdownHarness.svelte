<script lang="ts">
  import Dropdown from "../src/lib/components/Dropdown.svelte"
  import Modal from "../src/lib/components/Modal.svelte"
  let visible = $state(true)
  let open = $state(false)
  let selected = $state("two")
  const options = Array.from({ length: 12 }, (_, i) => ({ value: i === 1 ? "two" : String(i), label: "Option " + i }))
</script>

{#if visible}
  <Modal title="Dropdown test" onclose={() => visible = false}>
    <div style="height: 160px">Scrollable content</div>
    <Dropdown id="test-options" label="Mode" {options} {selected} {open}
      triggerClass="test-trigger" onOpenChange={value => open = value} onSelect={value => selected = value} />
    <button type="button" data-outside>Outside menu</button>
    <div style="height: 160px">More content</div>
  </Modal>
{/if}
