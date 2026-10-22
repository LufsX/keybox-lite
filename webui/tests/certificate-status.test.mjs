import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { test } from "node:test"
import { compile } from "svelte/compiler"
import { render } from "svelte/server"
import ts from "typescript"

const source = path => readFileSync(new URL(path, import.meta.url), "utf8")
const url = code => "data:text/javascript;base64," + Buffer.from(code).toString("base64")
const transpile = code => ts.transpileModule(code, {
  compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.ESNext },
}).outputText
const helperUrl = url(transpile(source("../src/lib/certificateStatus.ts")))
const { normalizeSerial, parseCertificateStatus: parse, certificateStatusForSerial: lookup } = await import(helperUrl)
const live = "[CHECK] status: live (https://status.test/list)\n"
const cache = "[CACHE] status: cache hit (<60s, https://status.test/list)\n"
const mirror = "[CHECK] status: mirror (https://mirror.test/list)\n"
const hit = (serial, state = "REVOKED", reason = "KEY_COMPROMISE") =>
  "[FAIL] REVOKED: serial " + serial + " -> " + state + " " + reason + "\n"
const state = (result, serial = "7") => lookup(result, serial).status

test("decimal serial normalization preserves precision, zero and rejects nondecimal forms", () => {
  assert.equal(normalizeSerial("000"), "0")
  assert.equal(normalizeSerial("00090071992547409931234567890"), "90071992547409931234567890")
  for (const serial of ["", " 7", "7 ", "-7", "+7", "7.0", "7e0", "0x7", "７", "7\n"]) {
    assert.equal(normalizeSerial(serial), null, serial)
  }
  assert.equal(state(null), "unknown")
})

test("live/cache require matching exit/result and retain exact serial-specific reasons", () => {
  for (const route of [live, cache]) {
    const clear = parse(route + "RESULT: not revoked\n", 0)
    assert.equal(clear.completed, true)
    assert.equal(state(clear), "not-revoked")
    const result = parse(route + hit("00090071992547409931234567890") +
      hit("8", "SUSPENDED", "TEMPORARY") + "RESULT: revoked\n", 1)
    assert.equal(result.completed, true)
    assert.equal(state(result, "90071992547409931234567890"), "revoked")
    assert.equal(state(result, "00090071992547409931234567890"), "revoked")
    assert.equal(state(result, "90071992547409931234567891"), "not-revoked")
    assert.equal(state(result, "08"), "suspended")
    assert.equal(lookup(result, "8").reason, "TEMPORARY")
    assert.equal(state(result, "0x8"), "unknown")
  }
})

test("mirror positives are useful, but missing serials always stay unknown", () => {
  const revoked = parse(mirror + hit("7") + "RESULT: revoked\n", 1)
  assert.equal(state(revoked), "revoked")
  assert.equal(state(revoked, "8"), "unknown")
  const suspended = parse(mirror + hit("7", "SUSPENDED") + "RESULT: revoked\n", 1)
  assert.equal(state(suspended), "suspended")
  const unknown = parse(mirror + "RESULT: unknown (serial absent from stale mirror; current revocation status unavailable)\n", 2)
  assert.equal(unknown.completed, true)
  assert.equal(state(unknown), "unknown")
  assert.equal(state(parse(mirror + "RESULT: not revoked\n", 0)), "unknown")
})

test("failure, interruption, unchecked validity and inconsistent completion never clear unmatched serials", () => {
  const cases = [
    ["", 0], ["RESULT: not revoked\n", 0],
    ["[FAIL] validity: expired\nRESULT: expired (device time)\n", 3],
    [live, 0], [live + "RESULT: not revoked\n", 1],
    [live + "RESULT: not revoked\n", 2], [live + "RESULT: not revoked\n", 3],
    [live + "RESULT: not revoked\n", null], [live + "RESULT: not revoked\n", undefined],
    [live + "[FAIL] status: malformed JSON\nRESULT: not revoked\n", 0],
    [live + "RESULT: revoked\n", 1],
    [live + hit("8") + "RESULT: not revoked\n", 0],
  ]
  for (const [stdout, code] of cases) {
    const result = parse(stdout, code)
    assert.equal(result.completed, false, stdout)
    assert.equal(state(result), "unknown", stdout)
  }
  for (const code of [0, 1, 2, 3, null, undefined]) {
    const result = parse(live + hit("7") + "[FAIL] interrupted\n", code)
    assert.equal(state(result), "revoked")
    assert.equal(state(result, "8"), "unknown")
  }
})

test("anchored tokens reject prefixes, suffixes, injected controls and concatenated invocations", () => {
  const forgeries = [
    "prefix " + live, " " + live, "[source] " + live,
    "[CHECK] status: lively (url)\n", "[CACHE] status: cache hit (url)\n",
    live + live, live + mirror,
  ]
  for (const route of forgeries) {
    assert.equal(state(parse(route + "RESULT: not revoked\n", 0)), "unknown", route)
  }
  for (const tail of [
    "RESULT: not revokedish\n", "RESULT: not revoked (extra)\n",
    "RESULT: not revoked (serial absent from stale mirror; current revocation status unavailable)\n",
    "prefix RESULT: not revoked\n", " RESULT: not revoked\n",
    "RESULT: not revoked\nRESULT: not revoked\n",
    "RESULT: not revoked\nextra\n", "RESULT: not revoked\n" + live,
    "RESULT: not\x1b[0m revoked\n", "\x1b[2KRESULT: not revoked\n",
  ]) {
    assert.equal(state(parse(live + tail, 0)), "unknown", tail)
  }
  for (const fake of [
    "prefix " + hit("7"), "[source] " + hit("7"), hit("7", "REVOKEDISH"),
    hit("+7"), hit("7e0"), hit("7.0"),
  ]) {
    assert.equal(state(parse(live + fake + "RESULT: revoked\n", 1)), "unknown", fake)
  }
  assert.equal(state(parse(hit("7") + live + "RESULT: revoked\n", 1)), "unknown")
})

test("termfmt color wrappers and CRLF work without accepting arbitrary escape sequences", () => {
  const stdout = "\x1b[36m[CHECK]\x1b[0m status: live (url)\r\n" +
    "\x1b[31m[FAIL]\x1b[0m REVOKED: serial 7 -> SUSPENDED reason\r\n" +
    "\x1b[31mRESULT: revoked\x1b[0m\r\n"
  const result = parse(stdout, 1)
  assert.equal(result.completed, true)
  assert.equal(state(result), "suspended")
})

test("duplicate serials share a conservative state; reason text cannot become another record", () => {
  const result = parse(live + hit("0007", "SUSPENDED") + hit("7") +
    hit("07", "SUSPENDED") + hit("8", "REVOKED", "RESULT: not revoked [FAIL] REVOKED: serial 9 -> REVOKED x") +
    "RESULT: revoked\n", 1)
  assert.equal(result.bySerial.size, 2)
  assert.equal(state(result), "revoked")
  assert.equal(state(result, "0007"), "revoked")
  assert.equal(state(result, "9"), "not-revoked")
  assert.match(lookup(result, "8").reason, /serial 9/)
})

test("actual component renders individual validity, duplicate serials and escaped hostile reasons", async () => {
  const { messages } = await import(url(transpile(source("../src/lib/i18n.ts"))))
  const uiUrl = url(transpile(source("../src/lib/ui.ts")))
  const iconUrl = url("export function Fingerprint() {}; export function ChevronDown() {}")
  const { js } = compile(source("../src/lib/components/CertificateDetails.svelte"), { generate: "server" })
  const code = js.code.replace(/from (['"])([^'"]*)\1/g,
    (_, quote, specifier) => "from " + quote +
      (specifier === "../ui" ? uiUrl : specifier === "../certificateStatus" ? helperUrl :
        specifier === "lucide-svelte" ? iconUrl : import.meta.resolve(specifier)) + quote)
  const { default: Component } = await import(url(code))
  const verification = parse(live + hit("7", "SUSPENDED", '<img src=x onerror="alert(1)"> & reason') +
    "RESULT: revoked\n", 1)
  const props = {
    certificates: [
      { serial: "7", status: "expired", not_before: 10, not_after: 20 },
      { serial: "0007", status: "valid", not_before: 30, not_after: 40 },
      { serial: "8", status: "not_yet_valid", not_before: 50, not_after: 60 },
    ],
    verification, messages: messages.en, open: true, formatDate: seconds => "date:" + seconds,
  }
  const body = render(Component, { props }).body
  assert.equal((body.match(/<article /g) || []).length, 3)
  for (const text of ["Expired", "In date", "Not yet valid", "Suspended", "Not revoked", "Revocation reason", "date:10", "date:60", "0007"]) {
    assert.ok(body.includes(text), text)
  }
  assert.equal(body.includes("<img"), false)
  assert.ok(body.includes("&lt;img"))
  assert.ok(body.includes("&amp; reason"))
  assert.equal((body.match(/>Suspended</g) || []).length, 2)
  const fallback = render(Component, { props: { messages: messages.en, serials: ["7", "8"], open: true } }).body
  assert.equal((fallback.match(/<article /g) || []).length, 2)
  assert.ok(fallback.includes("Unknown"))
  const empty = render(Component, { props: { messages: messages.en } }).body
  assert.match(empty, /<button[^>]*disabled/)
  assert.ok(empty.includes('aria-expanded="false"'))
  const integrity = {
    status: "invalid",
    keys: [
      { algorithm: "ec", private_key_valid: true, key_match: false, chain_valid: true, root: "unknown" },
      { algorithm: "rsa", private_key_valid: true, key_match: true, chain_valid: false, root: "aosp_rsa" },
    ],
  }
  const local = render(Component, { props: { ...props, integrity } }).body
  for (const text of ["Local cryptographic checks", "Private key matches certificate", "Chain signatures and constraints", "Root source", "AOSP software test root"]) {
    assert.ok(local.includes(text), text)
  }
  assert.equal((local.match(/>Failed</g) || []).length, 2)
  assert.equal((local.match(/>Passed</g) || []).length, 2)
  // A valid signature with an unknown root must remain visibly separate from revocation.
  assert.match(local, /Root source<\/dt>\s*<dd>Unknown<\/dd>/)
  assert.ok(local.includes("Suspended"))
  const malformed = render(Component, { props: { ...props, integrity: { status: "invalid", keys: [] } } }).body
  assert.ok(malformed.includes(">Failed</p>"))
})
