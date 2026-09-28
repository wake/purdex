import type { WorkerTheme } from './types'
import { PURDEX_THEME } from './purdex'

const registry = new Map<string, WorkerTheme>()

export function registerWorkerTheme(theme: WorkerTheme): void {
  registry.set(theme.id, theme)
}

/** Unknown or missing id falls back to `purdex` (spec §4.1). */
export function getWorkerTheme(id: string | undefined): WorkerTheme {
  const theme = id !== undefined ? registry.get(id) : undefined
  return theme ?? (registry.get('purdex') as WorkerTheme)
}

export function listWorkerThemes(): WorkerTheme[] {
  return [...registry.values()]
}

/** Maps a theme's vars to `--wt-<key>` custom properties, e.g. `{ '--wt-font-size': '14px', ... }`. */
export function workerThemeStyle(theme: WorkerTheme): Record<string, string> {
  const style: Record<string, string> = {}
  for (const [key, value] of Object.entries(theme.vars)) {
    style[`--wt-${key}`] = value
  }
  return style
}

registerWorkerTheme(PURDEX_THEME)
