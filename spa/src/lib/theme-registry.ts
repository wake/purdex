import type { ThemeTokens } from './theme-tokens'

export interface ThemeDefinition {
  id: string
  name: string
  tokens: ThemeTokens
  builtin: boolean
}

const registry = new Map<string, ThemeDefinition>()

export function registerTheme(def: ThemeDefinition): void {
  registry.set(def.id, def)
}

export function getTheme(id: string): ThemeDefinition | undefined {
  return registry.get(id)
}

export function getAllThemes(): ThemeDefinition[] {
  return [...registry.values()]
}

export function unregisterTheme(id: string): void {
  const theme = registry.get(id)
  if (theme && !theme.builtin) registry.delete(id)
}

export function clearThemeRegistry(): void {
  registry.clear()
}
