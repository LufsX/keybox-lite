import Pinyin from "tiny-pinyin"

const names = new Intl.Collator("en", { sensitivity: "base", numeric: true, ignorePunctuation: true })
const supported = Pinyin.isSupported()
const keys = new Map<string, string>()

function nameKey(label: string): string {
  const cached = keys.get(label)
  if (cached !== undefined) return cached
  const normalized = label.normalize("NFKC").trim()
  const key = supported ? Pinyin.convertToPinyin(normalized, "", true) : normalized
  if (keys.size >= 16384) keys.delete(keys.keys().next().value!)
  keys.set(label, key)
  return key
}

export function compareAppNames(a: string, b: string): number {
  return names.compare(nameKey(a), nameKey(b)) || names.compare(a, b)
}
