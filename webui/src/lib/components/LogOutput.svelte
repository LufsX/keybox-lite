<script lang="ts">
  import { Terminal } from "lucide-svelte"
  import type { LogLine, LogTone } from "../log"
  import { ui } from "../ui"

  interface Props {
    output: string
    lines: LogLine[]
    title: string
    hint: string
    viewSavedLogLabel: string
    disabled?: boolean
    onViewSavedLog: () => void
  }

  let { output, lines, title, hint, viewSavedLogLabel, disabled = false, onViewSavedLog }: Props = $props()

  const toneClasses: Record<LogTone, string> = {
    stage: "font-[650] text-[var(--accent)]",
    success: "font-[650] text-[var(--success)]",
    failure: "font-[650] text-[var(--log-failure)]",
    waiting: "font-[650] text-[var(--log-waiting)]",
    source: "font-[550] text-[var(--log-source)]",
  }
</script>

<section class={ui.panel}>
  <div class="mb-3 flex min-h-8 flex-wrap items-center justify-between gap-x-2 gap-y-1">
    <h2 class="grid min-w-0 max-w-full flex-[1_1_12rem] grid-cols-[17px_minmax(0,1fr)] items-start gap-1.5 text-sm font-semibold [&_svg]:mt-0.5 [&_svg]:text-[var(--muted)]">
      <Terminal size={17} /><span class="min-w-0 [overflow-wrap:anywhere]">{title}</span>
    </h2>
    <button class={ui.textButton + " max-w-full whitespace-normal text-start"} onclick={onViewSavedLog} {disabled}>
      <span class="min-w-0 [overflow-wrap:anywhere]">{viewSavedLogLabel}</span>
    </button>
  </div>
  <pre class="m-0 max-h-[300px] overflow-y-auto whitespace-pre-wrap rounded-lg bg-[var(--inset)] p-2 font-mono text-[11px] leading-normal text-[var(--text)] [overflow-wrap:anywhere]">{#if output}{#each lines as line (line)}{#each line.parts as part}<span class={part.tone ? toneClasses[part.tone] : undefined}>{part.text}</span>{/each}{/each}{:else}{hint}{/if}</pre>
</section>
