import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { test } from "node:test"
import { runInNewContext } from "node:vm"
import ts from "typescript"

const source = readFileSync(new URL("../src/lib/i18n.ts", import.meta.url), "utf8")
function load(navigator) {
  const exports = {}
  runInNewContext(ts.transpileModule(source, {
    compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.CommonJS },
  }).outputText, { exports, ...(navigator === undefined ? {} : { navigator }) })
  return exports
}
const { supportedLanguages, messages, isLocale, resolveLocale } = load()
const placeholders = value => [...value.matchAll(/\{\{?\s*[\w.]+\s*\}?\}|%(?:\d+\$)?[sdif]/g)].map(match => match[0]).sort()

test("every supported language has exactly the complete nonempty message and placeholder set", () => {
  const codes = Array.from(supportedLanguages, language => language.code)
  assert.deepEqual(codes, ["en", "zh", "zh-TW", "ja", "ko", "es", "ru"])
  assert.equal(new Set(codes).size, codes.length)
  assert.deepEqual(Object.keys(messages).sort(), [...codes].sort())
  const keys = Object.keys(messages.en).sort()
  for (const { code, nativeLabel } of supportedLanguages) {
    assert.ok(nativeLabel.trim(), code)
    assert.deepEqual(Object.keys(messages[code]).sort(), keys, code)
    for (const key of keys) {
      const value = messages[code][key]
      assert.equal(typeof value, "string", code + "." + key)
      assert.ok(value.trim(), code + "." + key)
      assert.deepEqual(placeholders(value), placeholders(messages.en[key]), code + "." + key)
    }
  }
})

test("certificate revocation states remain distinct from an unchecked status in every language", () => {
  for (const { code } of supportedLanguages) {
    const m = messages[code]
    const states = [m.revoked, m.certificateSuspended, m.notRevoked, m.certificateStatusUnknown, m.unchecked]
    for (const value of [...states, m.revocationReason]) {
      assert.equal(typeof value, "string", code)
      assert.ok(value.trim(), code)
    }
    assert.equal(new Set(states).size, states.length, code)
    assert.equal("certificateRevoked" in m, false, code + ": reuse revoked")
    assert.equal("certificateNotRevoked" in m, false, code + ": reuse notRevoked")
  }
})

test("stored locales accept only exact supported codes and reject object properties", () => {
  for (const { code } of supportedLanguages) assert.equal(isLocale(code), true, code)
  for (const value of [null, undefined, {}, [], 0, "en-US", "zh-tw", "", "toString", "__proto__", "constructor"]) {
    assert.equal(isLocale(value), false, String(value))
  }
})

test("browser language mapping preserves Chinese script and regional variants", () => {
  const cases = {
    zh: "zh", "zh-CN": "zh", "zh-SG": "zh", "zh-Hans": "zh",
    "zh-TW": "zh-TW", "zh-HK": "zh-TW", "zh-MO": "zh-TW",
    "zh-Hant": "zh-TW", "zh-Hant-CN": "zh-TW", "zh-Hans-TW": "zh",
    " ZH_hant_HK ": "zh-TW", "ja-JP": "ja", "ko-KR": "ko",
    "es-MX": "es", "es-ES": "es", "ru-RU": "ru", "en-GB": "en",
    "fr-FR": "en", "": "en", "toString": "en",
  }
  for (const [input, expected] of Object.entries(cases)) {
    assert.equal(resolveLocale(input), expected, input)
  }
})

test("detect handles browser language and defaults safely when navigator is absent", () => {
  assert.equal(load({ language: "zh-HK" }).detect(), "zh-TW")
  assert.equal(load({ language: "es-AR" }).detect(), "es")
  assert.equal(load({ language: "" }).detect(), "en")
  assert.equal(load().detect(), "en")
})
