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

// Recursively clone + freeze so `get()` consumers can't bypass persist/sync
// via `snapshot.nested.x = ...`. Primitives and already-frozen refs pass
// through untouched; plain objects and arrays are shallow-cloned so the
// returned graph is detached from store-internal state.
export function deepFreeze<T>(value: T): T {
  if (value === null || typeof value !== 'object') return value
  if (Object.isFrozen(value)) return value
  const clone: unknown = Array.isArray(value)
    ? (value as unknown[]).map((item) => deepFreeze(item))
    : Object.fromEntries(
        Object.entries(value as Record<string, unknown>).map(([k, v]) => [k, deepFreeze(v)]),
      )
  return Object.freeze(clone) as T
}
