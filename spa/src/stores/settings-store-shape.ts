type PlainRecord = Record<string, unknown>
type FlatModuleMap = Record<string, PlainRecord>
type ScopedModuleMap = Record<string, FlatModuleMap>

function isPlainRecord(value: unknown): value is PlainRecord {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function sanitizeFlatModuleMap(value: unknown): FlatModuleMap {
  if (!isPlainRecord(value)) return {}

  const healed: FlatModuleMap = {}
  for (const [moduleId, moduleValue] of Object.entries(value)) {
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
    if (!isPlainRecord(scopeValue)) continue
    healed[scopeId] = sanitizeFlatModuleMap(scopeValue)
  }
  return healed
}
