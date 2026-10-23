/** Mutable prefix sums: O(log n) measurement, offset lookup and range lookup. */
export class VirtualIndex {
  readonly keys: readonly string[]
  readonly positions: Map<string, number>
  private heights: number[]
  private tree: Float64Array

  constructor(keys: readonly string[], sizes: ReadonlyMap<string, number> = new Map(), estimate = 72) {
    this.keys = keys
    this.positions = new Map(keys.map((key, index) => [key, index]))
    const fallback = Number.isFinite(estimate) && estimate > 0 ? estimate : 72
    this.heights = keys.map(key => {
      const height = sizes.get(key)
      return height !== undefined && Number.isFinite(height) && height > 0 ? height : fallback
    })
    this.tree = new Float64Array(keys.length + 1)
    for (let i = 1; i <= keys.length; i++) {
      this.tree[i] += this.heights[i - 1]
      const parent = i + (i & -i)
      if (parent <= keys.length) this.tree[parent] += this.tree[i]
    }
  }

  offset(index: number): number {
    let sum = 0
    for (let i = Math.max(0, Math.min(this.keys.length, Math.floor(index))); i > 0; i -= i & -i) sum += this.tree[i]
    return sum
  }

  get total(): number { return this.offset(this.keys.length) }

  measure(index: number, height: number): boolean {
    if (!Number.isInteger(index) || index < 0 || index >= this.keys.length || !Number.isFinite(height) || height <= 0) return false
    const delta = height - this.heights[index]
    if (Math.abs(delta) < 0.25) return false
    this.heights[index] = height
    for (let i = index + 1; i < this.tree.length; i += i & -i) this.tree[i] += delta
    return true
  }

  /** Row containing offset; clamps overscroll to a real row. */
  at(offset: number): number {
    if (!this.keys.length) return 0
    const target = Number.isFinite(offset) ? Math.max(0, offset) : 0
    let index = 0, sum = 0
    let step = 1
    while (step * 2 <= this.keys.length) step *= 2
    for (; step >= 1; step /= 2) {
      const next = index + step
      if (next <= this.keys.length && sum + this.tree[next] <= target) {
        sum += this.tree[next]
        index = next
      }
    }
    return Math.min(index, this.keys.length - 1)
  }
}

export function virtualRange(index: VirtualIndex, scroll: number, viewport: number, overscan = 6): { start: number; end: number } {
  const count = index.keys.length
  if (!count) return { start: 0, end: 0 }
  const extra = Math.max(0, Math.floor(overscan) || 0)
  const top = Math.max(0, Math.min(Number.isFinite(scroll) ? scroll : 0, Math.max(0, index.total - Math.max(0, viewport))))
  return {
    start: Math.max(0, index.at(top) - extra),
    end: Math.min(count, index.at(top + Math.max(0, viewport)) + 1 + extra),
  }
}

/** Include one focused row without mounting every row between it and the viewport. */
export function virtualRows(start: number, end: number, focused: number | undefined): number[] {
  const rows = Array.from({ length: Math.max(0, end - start) }, (_, i) => start + i)
  if (focused !== undefined && focused >= 0 && (focused < start || focused >= end)) {
    if (focused < start) rows.unshift(focused)
    else rows.push(focused)
  }
  return rows
}
