import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { test } from "node:test"
import { runInNewContext } from "node:vm"
import ts from "typescript"

const module = { exports: {} }
runInNewContext(ts.transpileModule(readFileSync(new URL("../src/lib/dropdownPosition.ts", import.meta.url), "utf8"), {
  compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.CommonJS },
}).outputText, { exports: module.exports })
const { dropdownPosition } = module.exports
const anchor = (left, top, width = 120) => ({ left, top, width, right: left + width, bottom: top + 32 })
const viewport = { left: 0, top: 0, width: 360, height: 640 }

test("dropdown aligns to the trigger and never exceeds the viewport horizontally", () => {
  for (const left of [-40, 8, 180, 340]) {
    const p = dropdownPosition(anchor(left, 80, 200), { width: 120, height: 160 }, viewport)
    assert.ok(p.left >= 8)
    assert.ok(p.left + p.width <= 352)
    assert.equal(p.top, 116)
    assert.equal(p.flipped, false)
  }
})

test("dropdown flips above the trigger and constrains tall content", () => {
  const p = dropdownPosition(anchor(80, 570), { width: 120, height: 500 }, viewport)
  assert.equal(p.flipped, true)
  assert.equal(p.maxHeight, 320)
  assert.equal(p.top, 246)
})

test("visual viewport offsets and keyboard resize bound the available space", () => {
  const view = { left: 20, top: 100, width: 240, height: 180 }
  const p = dropdownPosition(anchor(200, 190, 300), { width: 300, height: 500 }, view)
  assert.equal(p.width, 224)
  assert.equal(p.left, 28)
  assert.ok(p.top >= 108)
  assert.ok(p.top + p.maxHeight <= 272)
  assert.equal(p.flipped, true)
})

test("empty viewport produces finite coordinates and zero available height", () => {
  const p = dropdownPosition(anchor(0, 0), { width: 120, height: 60 }, { left: 0, top: 0, width: 0, height: 0 })
  assert.equal(p.width, 0)
  assert.equal(p.maxHeight, 0)
  assert.ok(Number.isFinite(p.top))
})
