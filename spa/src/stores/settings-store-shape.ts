type PlainRecord = Record<string, unknown>
type FlatModuleMap = Record<string, PlainRecord>
type ScopedModuleMap = Record<string, FlatModuleMap>

// Keys that would trigger prototype pollution or shadow built-ins when copied
// with bracket assignment onto a plain object. Malformed persisted JSON can
// contain them as own enumerable properties, so we must drop them during heal.
const DANGEROUS_KEYS = new Set(['__proto__', 'prototype', 'constructor'])

function isPlainRecord(value: unknown): value is PlainRecord {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isSafeKey(key: string): boolean {
  return !DANGEROUS_KEYS.has(key)
}

export function sanitizeFlatModuleMap(value: unknown): FlatModuleMap {
  if (!isPlainRecord(value)) return {}

  const healed: FlatModuleMap = {}
  for (const [moduleId, moduleValue] of Object.entries(value)) {
    if (!isSafeKey(moduleId)) continue
    if (isPlainRecord(moduleValue)) {
      healed[moduleId] = moduleValue
    }
  }
  return healed
}

export function sanitizeScopedModuleMap(value: unknown): ScopedModuleMap {
  if (!isPlainRecord(value)) return {}

  const healed: ScopedModuleMap = {}
  for (const [scopeId, scopeValue] of Object.entries(value)) {
    if (!isSafeKey(scopeId)) continue
    if (!isPlainRecord(scopeValue)) continue
    healed[scopeId] = sanitizeFlatModuleMap(scopeValue)
  }
  return healed
}
