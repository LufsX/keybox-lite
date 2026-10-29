<script lang="ts">
  import VirtualList from "./VirtualList.svelte"

  let { names, label }: { names: readonly string[]; label: string } = $props()
  let scrollTarget = $state<HTMLDivElement>()
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users need to focus this scroll region.) -->
<div bind:this={scrollTarget} role="region" tabindex="0" aria-label={label + " (" + names.length + ")"}
  class="mt-1 h-36 min-w-0 overflow-y-auto overscroll-contain rounded-md border border-[var(--line)] px-1.5 text-[var(--muted)]"
  data-transfer-names>
  <div role="list">
    <VirtualList items={names} getKey={name => name} estimateSize={21} overscan={3} windowScroll={false} {scrollTarget}>
      {#snippet item(name, index)}
        <div role="listitem" aria-setsize={names.length} aria-posinset={index + 1}
          class="min-h-[21px] break-all py-0.5 font-mono text-[11px]">{name}</div>
      {/snippet}
    </VirtualList>
  </div>
</div>
