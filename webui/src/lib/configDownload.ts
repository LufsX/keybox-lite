import { exec, type ExecResult } from "./bridge"
import { shellQuote } from "./targets"

const MAX_EXPORT_BYTES = 1024 * 1024
const CHUNK_BYTES = 24 * 1024
const DOWNLOADS = "/sdcard/Download"

export class ConfigurationSaveError extends Error {
  constructor(public readonly code: "invalid" | "aborted" | "write") {
    super("Configuration export " + code)
    this.name = "ConfigurationSaveError"
  }
}

function alive(signal?: AbortSignal) {
  if (signal?.aborted) throw new ConfigurationSaveError("aborted")
}
function successful(result: ExecResult): void {
  if (result.failure || result.errno !== 0) throw new ConfigurationSaveError("write")
}
function encode(bytes: Uint8Array): string {
  let value = ""
  for (const byte of bytes) value += String.fromCharCode(byte)
  return btoa(value)
}
function nonce(): string {
  const bytes = new Uint8Array(12)
  crypto.getRandomValues(bytes)
  return [...bytes].map(byte => byte.toString(16).padStart(2, "0")).join("")
}

export async function saveConfiguration(filename: string, contents: string, signal?: AbortSignal): Promise<{ path: string | null }> {
  alive(signal)
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,119}\.json$/.test(filename)) throw new ConfigurationSaveError("invalid")
  let value: unknown
  try { value = JSON.parse(contents) } catch { throw new ConfigurationSaveError("invalid") }
  if (!value || typeof value !== "object" || Array.isArray(value) ||
      (value as Record<string, unknown>).format !== "keybox-lite.targets" ||
      (value as Record<string, unknown>).version !== 1) throw new ConfigurationSaveError("invalid")
  const bytes = new TextEncoder().encode(contents)
  if (!bytes.length || bytes.length > MAX_EXPORT_BYTES) throw new ConfigurationSaveError("invalid")
  const host = typeof window === "undefined" ? undefined : (window as unknown as { ksu?: { spawn?: unknown } }).ksu
  if (typeof host?.spawn !== "function") {
    const url = URL.createObjectURL(new Blob([bytes], { type: "application/json;charset=utf-8" }))
    const anchor = document.createElement("a")
    anchor.href = url
    anchor.download = filename
    try {
      document.body.append(anchor)
      alive(signal)
      anchor.click()
      // Keep the URL until the browser has consumed the click task.
      await new Promise<void>(resolve => setTimeout(resolve, 0))
    } finally { anchor.remove(); URL.revokeObjectURL(url) }
    return { path: null }
  }

  const token = nonce()
  const work = "/data/local/tmp/keybox-config-export." + token
  const data = work + "/config.json"
  const path = DOWNLOADS + "/" + filename.slice(0, -5) + "-" + token + ".json"
  let ownsWork = false
  try {
    // Let this short native command finish even after disposal so we can learn
    // whether it created the directory and release it.
    const created = await exec("umask 077\nmkdir " + shellQuote(work) + " || exit $?\nprintf '%s\\n' " + shellQuote(work) +
      "\n: > " + shellQuote(data))
    ownsWork = created.stdout.split("\n").includes(work)
    successful(created)
    if (!ownsWork) throw new ConfigurationSaveError("write")
    alive(signal)
    for (let offset = 0; offset < bytes.length; offset += CHUNK_BYTES) {
      const chunk = bytes.subarray(offset, offset + CHUNK_BYTES)
      const next = offset + chunk.length
      const result = await exec("printf %s " + shellQuote(encode(chunk)) + " | base64 -d >> " + shellQuote(data) +
        "\nrc=$?\n[ \"$rc\" -eq 0 ] || exit \"$rc\"\n[ \"$(wc -c < " + shellQuote(data) + ")\" -eq " + next + " ]")
      successful(result)
      alive(signal)
    }
    const published = await exec("umask 077\nexport_dir=" + shellQuote(DOWNLOADS) +
      "\n[ -d \"$export_dir\" ] || mkdir -p \"$export_dir\" || exit $?\n" +
      "stage=$(mktemp \"$export_dir/.keybox-lite-config.XXXXXXXX\") || exit $?\n" +
      "trap '[ -z \"$stage\" ] || rm -f \"$stage\"' 0\n" +
      "cat " + shellQuote(data) + " > \"$stage\" || exit $?\n" +
      "[ \"$(wc -c < \"$stage\")\" -eq " + bytes.length + " ] || exit 1\n" +
      "[ ! -e " + shellQuote(path) + " ] && [ ! -L " + shellQuote(path) + " ] || exit 1\n" +
      "mv \"$stage\" " + shellQuote(path) + " || exit $?\nstage=\nprintf '%s\\n' " + shellQuote(path))
    successful(published)
    if (published.stdout.trim() !== path) throw new ConfigurationSaveError("write")
    return { path }
  } finally {
    if (ownsWork) {
      try { await exec("rm -rf " + shellQuote(work)) } catch { /* Native cleanup can fail when the host is disposed. */ }
    }
  }
}
