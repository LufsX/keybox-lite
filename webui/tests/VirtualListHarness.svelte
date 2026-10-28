<script lang="ts">
  import { onMount } from "svelte"
  import VirtualList from "../src/lib/components/VirtualList.svelte"

  let { elementScroll = false }: { elementScroll?: boolean } = $props()
  let count = $state(4)
  let system = $state(false)
  let reverse = $state(false)
  let variable = $state(false)
  let visible = $state(true)
  let scrollTarget = $state<HTMLDivElement>()
  let items = $derived(Array.from({ length: count * 2 }, (_, i) => ({
    key: (i % 2 ? "sys-" : "user-") + Math.floor(i / 2),
    system: Boolean(i % 2),
    height: variable ? 42 + i % 3 * 14 : 56,
  })).filter(row => system || !row.system))
  let ordered = $derived(reverse ? [...items].reverse() : items)

  onMount(() => {
    const host = window as typeof window & { virtualTest?: unknown }
    host.virtualTest = {
      system: (value: boolean) => { system = value },
      count: (value: number) => { count = value },
      reverse: (value: boolean) => { reverse = value },
      variable: (value: boolean) => { variable = value },
      unmount: () => { visible = false },
    }
    return () => { delete host.virtualTest }
  })
</script>

<div class:element-scroll={elementScroll} bind:this={scrollTarget} data-scroll>
  <div style="height: 36px">List header</div>
  {#if visible}
    <VirtualList items={ordered} getKey={row => row.key} estimateSize={56} overscan={3}
      windowScroll={!elementScroll} scrollTarget={elementScroll ? scrollTarget : undefined}>
      {#snippet item(row)}
        <button data-item={row.key} style:height={row.height + "px"}
          style="width: 100%; display: block; box-sizing: border-box; margin: 0; border: 1px solid #aaa;">
          {row.key}
        </button>
      {/snippet}
    </VirtualList>
  {/if}
  <div data-tail style="height: 120px">Trailing content</div>
</div>

<style>
  .element-scroll { height: 320px; overflow: auto; scroll-padding-top: 12px; }
</style>
