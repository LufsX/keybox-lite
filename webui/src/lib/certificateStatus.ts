export interface KeyIntegrity {
  algorithm: "ec" | "rsa"
  private_key_valid: boolean
  key_match: boolean
  chain_valid: boolean
  root: "google" | "aosp_ec" | "aosp_rsa" | "knox" | "unknown"
}
export interface KeyboxIntegrity {
  status: "valid" | "invalid" | "unknown"
  keys: readonly KeyIntegrity[]
}

export type ValidityStatus = "valid" | "expired" | "not_yet_valid" | "unknown"
export interface CertificateValidity {
  serial: string
  not_before: number
  not_after: number
  status: ValidityStatus
}
export type CertificateRevocationStatus = "revoked" | "suspended" | "not-revoked" | "unknown"
export interface CertificateStatus {
  status: CertificateRevocationStatus
  reason?: string
}
export interface CertificateVerification {
  source: "live" | "cache" | "mirror" | "unknown"
  completed: boolean
  result: "revoked" | "not revoked" | "unknown"
  bySerial: ReadonlyMap<string, CertificateStatus>
}

/** Decimal text only: never round certificate serials through Number. */
export function normalizeSerial(serial: string): string | null {
  return /^[0-9]+$/.test(serial) ? serial.replace(/^0+(?=\d)/, "") : null
}

/**
 * Pass only the stdout and exit code of one finished verify invocation.
 * Do not pass saved logs, streamed partial output, or combined invocations.
 * A missing exit code represents an interrupted/failed bridge invocation.
 */
export function parseCertificateStatus(stdout: string, exitCode: number | null | undefined): CertificateVerification {
  const bySerial = new Map<string, CertificateStatus>()
  let source: CertificateVerification["source"] = "unknown"
  let sourceCount = 0
  let resultCount = 0
  let result: CertificateVerification["result"] = "unknown"
  let failed = false
  let terminal = false
  let trailing = false
  for (const raw of stdout.split("\n")) {
    // Accept only the color wrappers emitted by termfmt, not arbitrary terminal controls.
    const line = raw.replace(/\r$/, "").replace(
      /^\x1b\[(?:31|32|33|35|36)m(\[(?:OK|FAIL|WARN|CHECK|CACHE)\]|RESULT: (?:not revoked|revoked|unknown))\x1b\[0m/,
      "$1",
    )
    if (!line) continue
    if (terminal) trailing = true
    if (/[\x00-\x1f\x7f-\x9f]/.test(line)) { failed = true; continue }
    const route = /^(?:\[CHECK\] status: (live|mirror) \([^()]+\)|\[CACHE\] status: cache hit \(<60s, [^()]+\))$/.exec(line)
    if (route) {
      sourceCount++
      source = route[1] === "live" ? "live" : route[1] === "mirror" ? "mirror" : "cache"
      continue
    }
    const hit = /^\[FAIL\] REVOKED: serial ([0-9]+) -> (REVOKED|SUSPENDED)(?: (.*))?$/.exec(line)
    if (hit && sourceCount === 1 && !terminal) {
      const serial = normalizeSerial(hit[1])!
      const status = hit[2] === "REVOKED" ? "revoked" : "suspended"
      const previous = bySerial.get(serial)
      // Conflicting duplicate reports retain the more restrictive state.
      if (!previous || previous.status !== "revoked") {
        bySerial.set(serial, { status, ...(hit[3] ? { reason: hit[3] } : {}) })
      }
      continue
    }
    if (line.startsWith("[FAIL]") || /^\[(?:CHECK|CACHE)\] status:/.test(line)) failed = true
    const summary = /^RESULT: (revoked|not revoked|unknown(?: \(serial absent from stale mirror; current revocation status unavailable\))?)$/.exec(line)
    if (summary) {
      resultCount++
      result = summary[1].startsWith("unknown") ? "unknown" : summary[1] as CertificateVerification["result"]
      terminal = true
    } else if (line.startsWith("RESULT:")) {
      failed = true
    }
  }
  const consistent = (result === "not revoked" && exitCode === 0 && bySerial.size === 0) ||
    (result === "revoked" && exitCode === 1 && bySerial.size > 0) ||
    (result === "unknown" && exitCode === 2)
  const completed = sourceCount === 1 && resultCount === 1 && !failed && !trailing && consistent
  return { source: sourceCount === 1 ? source : "unknown", completed, result, bySerial }
}

export function certificateStatusForSerial(
  verification: CertificateVerification | null | undefined,
  serial: string,
): CertificateStatus {
  const normalized = normalizeSerial(serial)
  if (!verification || normalized === null) return { status: "unknown" }
  const hit = verification.bySerial.get(normalized)
  if (hit) return hit
  if (verification.completed && (verification.source === "live" || verification.source === "cache") &&
      (verification.result === "not revoked" || verification.result === "revoked")) {
    return { status: "not-revoked" }
  }
  return { status: "unknown" }
}
