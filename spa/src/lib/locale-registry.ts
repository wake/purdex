export interface LocaleDef {
  id: string
  name: string
  translations: Record<string, string>
  builtin: boolean
}

const registry = new Map<string, LocaleDef>()

export function registerLocale(def: LocaleDef): void {
  registry.set(def.id, def)
}

export function getLocale(id: string): LocaleDef | undefined {
  return registry.get(id)
}

export function getAllLocales(): LocaleDef[] {
  return [...registry.values()]
}

export function unregisterLocale(id: string): void {
  const locale = registry.get(id)
  if (locale && !locale.builtin) registry.delete(id)
}

export function clearLocaleRegistry(): void {
  registry.clear()
}
