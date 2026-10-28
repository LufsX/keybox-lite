import { exec } from "./bridge"

const MAX_OUTPUT = 1_048_576
const PACKAGE = /^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$/

export type InstallTimesFailure = "aborted" | "unavailable" | "bridge" | "timeout" | "command" | "output"
export class InstallTimesError extends Error {
  constructor(public readonly code: InstallTimesFailure, message: string) {
    super(message)
    this.name = "InstallTimesError"
  }
}

// Keep the full timestamp, including its space and optional numeric time zone.
// Android versions without a zone print device-local wall time.
function timestamp(value: string): number | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,3}))?(?:\s*(Z|UTC|GMT|(?:GMT)?[+-]\d{2}:?\d{2}))?$/.exec(value.trim())
  if (!match) return null
  const [, year, month, day, hour, minute, second, fraction, zone] = match
  const y = Number(year), m = Number(month), d = Number(day), h = Number(hour), min = Number(minute), sec = Number(second)
  if (y < 1970 || m < 1 || m > 12 || d < 1 || d > new Date(Date.UTC(y, m, 0)).getUTCDate() ||
      h > 23 || min > 59 || sec > 59) return null
  const ms = Number((fraction ?? "").padEnd(3, "0"))
  let result: number
  if (zone) {
    const offset = /([+-])(\d{2}):?(\d{2})$/.exec(zone)
    if (offset && (Number(offset[2]) > 23 || Number(offset[3]) > 59)) return null
    const minutes = offset ? (Number(offset[2]) * 60 + Number(offset[3])) * (offset[1] === "+" ? 1 : -1) : 0
    result = Date.UTC(y, m - 1, d, h, min, sec, ms) - minutes * 60_000
  } else {
    const date = new Date(y, m - 1, d, h, min, sec, ms)
    if (date.getFullYear() !== y || date.getMonth() !== m - 1 || date.getDate() !== d ||
        date.getHours() !== h || date.getMinutes() !== min || date.getSeconds() !== sec) return null
    result = date.getTime()
  }
  return Number.isFinite(result) && result > 0 ? result : null
}

/** Parse raw dumpsys or the field-only output produced by installTimesCommand. */
export function parseInstallTimes(raw: string): Map<string, number> {
  if (raw.length > MAX_OUTPUT) throw new InstallTimesError("output", "Package install time output exceeds limit")
  if (/DUMP TIMEOUT|Permission Denial|Error dumping service/.test(raw))
    throw new InstallTimesError("output", "Incomplete package install time output")
  const records = new Map<string, { time?: number; users: Map<string, { time?: number; installed: boolean }> }>()
  let current: ReturnType<typeof records.get>
  let user: string | undefined
  let userIndent = -1
  for (const line of raw.split(/\r?\n/)) {
    const header = /^\s*Package \[([^\]]+)\]/.exec(line)
    if (header) {
      current = undefined
      user = undefined
      if (PACKAGE.test(header[1]) && header[1].length <= 255) {
        // Disabled/hidden system package records may follow the active package.
        // The first record describes the active installation.
        if (!records.has(header[1])) {
          current = { users: new Map() }
          records.set(header[1], current)
        }
      }
      continue
    }
    if (!current) continue
    const child = /^(\s*)User (\d+):\s*(.*)$/.exec(line)
    if (child) {
      user = String(Number(child[2]))
      userIndent = child[1].length
      current.users.set(user, { installed: !/\binstalled=false\b/.test(child[3]) })
    } else if (line.trim() && line.search(/\S/) <= userIndent) {
      user = undefined
      userIndent = -1
    }
    const field = /\bfirstInstallTime=(.*?)(?=\s+[A-Za-z][A-Za-z0-9_]*=|$)/.exec(line)
    if (!field) continue
    const time = timestamp(field[1])
    if (time === null) continue
    if (user !== undefined) current.users.get(user)!.time = time
    else current.time = time
  }
  if (!records.size) throw new InstallTimesError("output", "No package records in install time output")
  const result = new Map<string, number>()
  for (const [base, record] of records) {
    const time = record.users.get("0")?.time ?? record.time
    if (time !== undefined && record.users.get("0")?.installed !== false) result.set(base, time)
    for (const [user, child] of record.users) {
      const time = child.time ?? record.time
      if (child.installed && time !== undefined) result.set(base + "@" + user, time)
    }
  }
  return result
}

/**
 * One dumpsys invocation. No requested names enter shell code.
 * The upstream marker preserves dumpsys failure through the pipeline; awk caps
 * output and rejects missing completion markers, including truncated streams.
 */
export function installTimesCommand(): string {
  return `{ dumpsys package packages 2>/dev/null; result=$?; printf '\\n__KEYBOX_INSTALL_TIMES_DONE__:%s\\n' "$result"; } | awk '
function output() {
  size += length($0) + 1
  if (size > ${MAX_OUTPUT} || length($0) > 4096) { bad=1; exit 1 }
  print
}
/DUMP TIMEOUT|Permission Denial|Error dumping service/ { bad=1 }
/^__KEYBOX_INSTALL_TIMES_DONE__:/ { done=1; if ($0 != "__KEYBOX_INSTALL_TIMES_DONE__:0") bad=1; next }
/^[[:space:]]*Package \\[/ || /^[[:space:]]*User [0-9]+:/ || /firstInstallTime=/ { output() }
END { if (!done || bad) exit 1 }
'`
}

/** Unknown times are absent. Operational failures reject; aborted reads publish nothing. */
export async function readInstallTimes(signal?: AbortSignal): Promise<Map<string, number>> {
  if (signal?.aborted) throw new InstallTimesError("aborted", "Install time request aborted")
  const result = await exec(installTimesCommand(), undefined, 15_000, signal).catch(() => {
    throw new InstallTimesError(signal?.aborted ? "aborted" : "bridge", "Unable to read package install times")
  })
  if (signal?.aborted) throw new InstallTimesError("aborted", "Install time request aborted")
  if (result.failure) throw new InstallTimesError(result.failure, "Unable to read package install times: " + result.failure)
  if (result.errno !== 0) throw new InstallTimesError("command", "Package install time command failed")
  return parseInstallTimes(result.stdout)
}
