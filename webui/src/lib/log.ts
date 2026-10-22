export type LogTone = "stage" | "success" | "failure" | "waiting" | "source"
export interface LogPart { text: string; tone?: LogTone }
export interface LogLine { text: string; parts: LogPart[] }

const tones: Record<string, LogTone> = {
  FETCH: "stage", CHECK: "stage", CACHE: "stage", INSTALL: "stage",
  DONE: "success", OK: "success", FAIL: "failure", WARN: "waiting", WAIT: "waiting", STOP: "waiting",
}

// Remove terminal formatting, hyperlinks and control strings before displaying text.
export function stripAnsi(text: string): string {
  return text
    .replace(/(?:\x1b\]|\x9d)[^\x07\x1b\x9c]*(?:\x07|\x1b\\|\x9c|$)/g, "")
    .replace(/(?:\x1b[P^_X]|[\x90\x98\x9e\x9f])[^\x1b\x9c]*(?:\x1b\\|\x9c|$)/g, "")
    .replace(/(?:\x1b\[|\x9b)[0-?]*[ -/]*[@-~]/g, "")
    .replace(/\x1b[ -/]*[@-Z\\-~]/g, "")
    .replace(/(?:\x1b\[|\x9b)[0-?]*[ -/]*$/g, "")
    .replace(/[\x00-\x08\x0b-\x1f\x7f-\x9f]/g, "")
}

export function parseLogLine(text: string): LogLine {
  const parts: LogPart[] = []
  const tokens = /\[(FETCH|CHECK|CACHE|INSTALL|DONE|OK|FAIL|WARN|WAIT|STOP)\]|\[(?:[\w.-]+\s*\/\s*[\w.-]+|meow|spectre|addon)\]|\bRESULT:\s*(unknown|not revoked|revoked)\b/gi
  let offset = 0
  for (const match of text.matchAll(tokens)) {
    const index = match.index!
    if (index > offset) parts.push({ text: text.slice(offset, index) })
    const tone = match[1] ? tones[match[1].toUpperCase()]
      : match[2] ? (match[2].toLowerCase() === "not revoked" ? "success" : match[2].toLowerCase() === "revoked" ? "failure" : "waiting")
      : "source"
    parts.push({ text: match[0], tone })
    offset = index + match[0].length
  }
  if (offset < text.length) parts.push({ text: text.slice(offset) })
  return { text, parts }
}

// Keep parsed rows intact across batches; only an unfinished tail and trimmed head change.
export class LogBuffer {
  text = ""
  lines: LogLine[] = []

  constructor(readonly limit = 80_000) {}

  replace(text: string) {
    this.text = ""
    this.lines = []
    this.append(text)
  }

  append(text: string) {
    const clean = stripAnsi(text)
    if (!clean) return
    let excess = clean.length >= this.limit ? 0 : Math.max(0, this.text.length + clean.length - this.limit)
    let tail = clean.slice(-this.limit)
    if (clean.length >= this.limit) {
      this.lines = []
    } else if (this.lines.length && !this.lines[this.lines.length - 1].text.endsWith("\n")) {
      tail = this.lines.pop()!.text + tail
    }
    this.text = (this.text + clean).slice(-this.limit)
    for (const line of tail.match(/[^\n]*\n|[^\n]+$/g) ?? []) {
      this.lines.push(parseLogLine(line))
    }
    let removed = 0
    while (excess > 0 && this.lines[removed].text.length <= excess) {
      excess -= this.lines[removed++].text.length
    }
    if (removed) this.lines.splice(0, removed)
    if (excess > 0) this.lines[0] = parseLogLine(this.lines[0].text.slice(excess))
  }
}
