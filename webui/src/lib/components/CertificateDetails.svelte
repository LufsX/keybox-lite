<script lang="ts">
  import { ChevronDown, Fingerprint } from "lucide-svelte"
  import { ui } from "../ui"
  import { certificateStatusForSerial } from "../certificateStatus"
  import type { CertificateValidity, CertificateVerification, CertificateRevocationStatus, ValidityStatus, KeyboxIntegrity } from "../certificateStatus"
  import type { messages as translations } from "../i18n"

  interface Props {
    certificates?: readonly CertificateValidity[]
    serials?: readonly string[]
    verification?: CertificateVerification | null
    integrity?: KeyboxIntegrity
    messages: Pick<typeof translations.en,
      "certificateDetails" | "certificate" | "deviceTimeNote" | "notBefore" | "notAfter" |
      "timeValid" | "expired" | "notYetValid" | "validityUnknown" | "revocationStatus" |
      "revoked" | "certificateSuspended" | "notRevoked" | "certificateStatusUnknown" | "revocationReason" |
      "localValidation" | "privateKeyMatch" | "chainSignatures" | "rootSource" | "validationPassed" | "validationFailed" | "softwareTestRoot">
    formatDate?: (seconds: number) => string
    open?: boolean
    id?: string
  }

  let {
    certificates = [], serials = [], verification = null, integrity, messages: m,
    formatDate = (seconds: number) => new Date(seconds * 1000).toLocaleString(),
    open = $bindable(false), id = "certificate-details",
  }: Props = $props()

  const badge = "inline-flex max-w-full rounded-md px-1.5 py-0.5 text-[11px] leading-snug"
  const neutral = " bg-[var(--inset)] text-[var(--muted)]"
  const danger = " bg-[var(--red-bg)] text-[var(--red)]"
  const good = " bg-[var(--success-bg)] text-[var(--success)]"
  const warning = " bg-[var(--warning-bg)] text-[var(--warning)]"
  const rows = $derived(certificates.length ? certificates : serials.map(serial => ({
    serial, not_before: null, not_after: null, status: "unknown" as const,
  })))
  function validityLabel(status: ValidityStatus) {
    return status === "valid" ? m.timeValid : status === "expired" ? m.expired :
      status === "not_yet_valid" ? m.notYetValid : m.validityUnknown
  }
  function revocationLabel(status: CertificateRevocationStatus) {
    return status === "revoked" ? m.revoked : status === "suspended" ? m.certificateSuspended :
      status === "not-revoked" ? m.notRevoked : m.certificateStatusUnknown
  }
</script>

<section class={ui.panel + " !p-0"}>
  <button class={ui.panelRow + " [&_svg]:text-[var(--muted)] disabled:opacity-50"}
    onclick={() => (open = !open)} aria-expanded={open} aria-controls={id} disabled={!rows.length}>
    <span class="flex min-w-0 items-center gap-2"><Fingerprint size={17} />{m.certificateDetails}</span>
    <span class="flex shrink-0 items-center gap-2"><span class={badge + neutral}>{rows.length || "—"}</span><ChevronDown size={16} class={"transition-transform duration-200 motion-reduce:transition-none " + (open ? "rotate-180" : "")} /></span>
  </button>
  <div {id} class={"grid transition-[grid-template-rows,opacity] duration-200 motion-reduce:transition-none " + (open ? "grid-rows-[1fr] opacity-100" : "grid-rows-[0fr] opacity-0")} inert={!open}>
    <div class="min-h-0 overflow-hidden">
      <div class="px-[var(--content-padding)] pb-[var(--content-padding)]">
        <p class="text-[11px] leading-relaxed text-[var(--muted)]">{m.deviceTimeNote}</p>
        {#if integrity}
          <div class="mt-2 min-w-0 rounded-lg bg-[var(--inset)] p-2 text-[11px] leading-relaxed">
            <h3 class="font-semibold">{m.localValidation}</h3>
            {#if !integrity.keys.length}
              <p class="mt-1 text-[var(--red)]">{integrity.status === "invalid" ? m.validationFailed : m.certificateStatusUnknown}</p>
            {/if}
            {#each integrity.keys as key, index}
              <div class="mt-2 border-t border-[var(--line)] pt-2 [overflow-wrap:anywhere]">
                <p class="font-semibold">Key {index + 1} · {key.algorithm.toUpperCase()}</p>
                <dl class="mt-1 grid grid-cols-[minmax(0,1fr)_auto] gap-x-2 gap-y-1">
                  <dt class="text-[var(--muted)]">{m.privateKeyMatch}</dt>
                  <dd class={key.private_key_valid && key.key_match ? "text-[var(--success)]" : "text-[var(--red)]"}>{key.private_key_valid && key.key_match ? m.validationPassed : m.validationFailed}</dd>
                  <dt class="text-[var(--muted)]">{m.chainSignatures}</dt>
                  <dd class={key.chain_valid ? "text-[var(--success)]" : "text-[var(--red)]"}>{key.chain_valid ? m.validationPassed : m.validationFailed}</dd>
                  <dt class="text-[var(--muted)]">{m.rootSource}</dt>
                  <dd>{key.root === "google" ? "Google" : key.root === "knox" ? "Samsung Knox" : key.root === "aosp_ec" ? "AOSP EC" : key.root === "aosp_rsa" ? "AOSP RSA" : m.certificateStatusUnknown}</dd>
                </dl>
                {#if key.root === "aosp_ec" || key.root === "aosp_rsa"}
                  <p class="mt-1 text-[var(--muted)]">{m.softwareTestRoot}</p>
                {/if}
              </div>
            {/each}
          </div>
        {/if}
        {#each rows as cert, index}
          {@const revocation = certificateStatusForSerial(verification, cert.serial)}
          <article class="mt-2 min-w-0 border-t border-[var(--line)] pt-2">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <h3 class="text-xs font-[550]">{m.certificate} {index + 1}</h3>
              <span class={badge + (cert.status === "valid" ? good : cert.status === "expired" || cert.status === "not_yet_valid" ? warning : neutral)}>{validityLabel(cert.status)}</span>
            </div>
            <p class="mt-1.5 select-text font-mono text-[11px] text-[var(--muted)] [overflow-wrap:anywhere]">{cert.serial}</p>
            <dl class="mt-2 grid grid-cols-2 gap-1.5 max-[359px]:grid-cols-1 [&_dt]:text-[11px] [&_dt]:leading-snug [&_dt]:text-[var(--muted)] [&_dd]:mt-0.5 [&_dd]:text-[11px] [&_dd]:leading-relaxed [&_dd]:tabular-nums [&_dd]:[overflow-wrap:anywhere]">
              <div><dt>{m.notBefore}</dt><dd>{cert.not_before === null ? "—" : formatDate(cert.not_before)}</dd></div>
              <div><dt>{m.notAfter}</dt><dd>{cert.not_after === null ? "—" : formatDate(cert.not_after)}</dd></div>
              <div class="col-span-full"><dt>{m.revocationStatus}</dt><dd><span class={badge + (revocation.status === "revoked" || revocation.status === "suspended" ? danger : revocation.status === "not-revoked" ? good : neutral)}>{revocationLabel(revocation.status)}</span></dd></div>
              {#if revocation.reason}
                <div class="col-span-full"><dt>{m.revocationReason}</dt><dd class="select-text whitespace-pre-wrap">{revocation.reason}</dd></div>
              {/if}
            </dl>
          </article>
        {/each}
      </div>
    </div>
  </div>
</section>
