<script lang="ts">
  let { label, icon = null }: { label: string; icon?: string | null } = $props()
  let failed = $state(false)
  $effect(() => { icon; failed = false })
</script>

<span class="grid size-9 shrink-0 place-items-center overflow-hidden rounded-full bg-[var(--inset)] text-sm text-[var(--muted)]" aria-hidden="true">
  {#if icon && !failed}
    <img src={icon} alt="" class="size-full object-contain" loading="lazy" onerror={() => failed = true} />
  {:else}
    {Array.from(label.trim())[0]?.toLocaleUpperCase() ?? "?"}
  {/if}
</span>
