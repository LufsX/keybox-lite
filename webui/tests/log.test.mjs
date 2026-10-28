import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { test } from "node:test"
import { runInNewContext } from "node:vm"
import ts from "typescript"

const source = readFileSync(new URL("../src/lib/log.ts", import.meta.url), "utf8")
const module = { exports: {} }
runInNewContext(ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.CommonJS },
}).outputText, { exports: module.exports })
const { LogBuffer, parseLogLine, stripAnsi } = module.exports
const flatten = (lines) => lines.map(line => line.parts.map(part => part.text).join("")).join("")

test("stages, warnings, source routes and verification results retain exact text", () => {
  const text = "[meow / gh-proxy] [FETCH][CHECK][CACHE][INSTALL][DONE][OK][FAIL][WARN][WAIT][STOP] 中文 <tag> &\n" +
    "[spectre / c.etc.moe] RESULT: unknown\n[addon / direct] RESULT: revoked\nRESULT: not revoked\n"
  const line = parseLogLine(text)
  assert.equal(flatten([line]), text)
  assert.deepEqual(Array.from(line.parts.filter(p => p.tone), p => p.tone), [
    "source", "stage", "stage", "stage", "stage", "success", "success", "failure", "waiting", "waiting", "waiting",
    "source", "waiting", "source", "failure", "success",
  ])
  assert.equal(parseLogLine("[unrelated] RESULT: unknownish").parts.some(p => p.tone), false)
})

test("accelerated route labels use the full hostname and keep the download URL intact", () => {
  const runtime = readFileSync(new URL("../../src/internal/runtime/runtime.go", import.meta.url), "utf8")
  assert.match(runtime, /accel:\s*"https:\/\/c\.etc\.moe\/"/)
  const parsed = parseLogLine("[meow / c.etc.moe] [FETCH] https://c.etc.moe/https://example.test/key")
  assert.equal(flatten([parsed]), "[meow / c.etc.moe] [FETCH] https://c.etc.moe/https://example.test/key")
  assert.equal(parsed.parts.find(part => part.tone === "source")?.text, "[meow / c.etc.moe]")
})

test("ANSI SGR, cursor codes, OSC links, control strings and CR never leak into logs", () => {
  const input = "\x1b[31m[FAIL]\x1b[0m\r\n\x1b[2K" +
    "\x1b]8;;https://example.test\x07link\x1b]8;;\x1b\\ " +
    "\x1bPprivate control data\x1b\\[WARN]\x9b33m yellow\x9b0m\t中文"
  assert.equal(stripAnsi(input), "[FAIL]\nlink [WARN] yellow\t中文")
  assert.equal(stripAnsi("visible\x1b[31"), "visible")
  assert.equal(stripAnsi("visible\x1b]0;unfinished title"), "visible")
})

test("incremental appends reuse completed rows and repair unfinished tokens and trimmed heads", () => {
  const buffer = new LogBuffer(60)
  buffer.append("[OK] first\n[WAR")
  const first = buffer.lines[0]
  buffer.append("N] second\n")
  assert.equal(buffer.lines[0], first)
  assert.equal(buffer.lines[1].parts[0].tone, "waiting")
  let expected = buffer.text
  for (let i = 0; i < 100; i++) {
    const chunk = i % 3 ? "[meow / direct] [FETCH] " + i + "\n" : "partial"
    expected = (expected + chunk).slice(-60)
    buffer.append(chunk)
    assert.equal(buffer.text, expected)
    assert.equal(flatten(buffer.lines), expected)
  }
  buffer.append("x".repeat(90_000) + "\n[OK] end")
  assert.equal(buffer.text.length, 60)
  assert.equal(flatten(buffer.lines), buffer.text)
  buffer.replace("[WARN] replacement\n")
  assert.equal(buffer.lines.length, 1)
  buffer.replace("")
  assert.equal(buffer.text, "")
  assert.equal(buffer.lines.length, 0)
})

test("default buffer caps visible and copied text at 80k including multiline ANSI input", () => {
  const buffer = new LogBuffer()
  buffer.append(("\x1b[32m[OK]\x1b[0m 中文 & <value>\n").repeat(6_000))
  assert.equal(buffer.text.length, 80_000)
  assert.equal(flatten(buffer.lines), buffer.text)
  assert.equal(buffer.text.includes("\x1b"), false)
})

test("history separates installed records and preserves unfamiliar formats as raw text", () => {
  const parse = module.exports.parseHistoryRecord
  const value = parse("2026-10-28 12:00:00 | meow | 4096 bytes | installed")
  assert.equal(value.time, "2026-10-28 12:00:00")
  assert.equal(value.source, "meow")
  assert.equal(value.bytes, 4096)
  for (const line of ["legacy entry", "2026-10-28 12:00:00 | meow | 4 bytes | failed", "2026-10-28 12:00:00 | meow | 999999999999999999 bytes | installed", "2026-10-28 12:00:00 |   | 4 bytes | installed"]) assert.equal(parse(line), null)
})
