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
// via `snapshot.nested.x = ...`. We never short-circuit on `Object.isFrozen`:
// a shallow-frozen input (e.g. `Object.freeze({ nested: {...} })`) would still
// leak a mutable nested ref — the whole point is that the *returned* graph is
// fully frozen regardless of input shape. The WeakMap maps each original
// object to its frozen clone, so cycles AND alias graphs (e.g. the same
// nested object referenced from two parent slots) both resolve to the
// frozen clone instead of leaking the original mutable ref.
export function deepFreeze<T>(value: T, seen: WeakMap<object, unknown> = new WeakMap()): T {
  if (value === null || typeof value !== 'object') return value
  const cached = seen.get(value as object)
  if (cached !== undefined) return cached as T

  const clone: unknown = Array.isArray(value) ? [] : {}
  // Register before recursing so cycles and aliases see this clone instead of
  // the original mutable ref.
  seen.set(value as object, clone)

  if (Array.isArray(value)) {
    const arr = clone as unknown[]
    for (let i = 0; i < (value as unknown[]).length; i++) {
      arr[i] = deepFreeze((value as unknown[])[i], seen)
    }
  } else {
    const obj = clone as Record<string, unknown>
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      obj[k] = deepFreeze(v, seen)
    }
  }

  return Object.freeze(clone) as T
}
