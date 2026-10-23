<div aria-hidden="true" data-skeleton="apps">
  {#each [1, 2, 3, 4, 5, 6, 7, 8] as placeholder}
    <div class="pb-1">
      <div class="flex min-h-16 items-center gap-1 rounded-[10px] bg-[var(--panel)] px-2 py-2">
        <div class="flex min-w-0 flex-1 items-center gap-2">
          <span class="skeleton-block size-9 shrink-0 !rounded-full"></span>
          <span class="grid min-w-0 flex-1 gap-2"><span class="skeleton-block h-3 w-1/2"></span><span class="skeleton-block h-2.5 w-3/4"></span></span>
        </div>
        <span class="size-8 shrink-0"></span>
        <span class="check-hit"><span class="skeleton-block size-[18px] !rounded-[5px]"></span></span>
      </div>
    </div>
  {/each}
</div>
