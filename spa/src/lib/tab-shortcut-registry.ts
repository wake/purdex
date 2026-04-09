import type { Tab, Pane } from '../types/tab'

export type TabShortcutHandler = (tab: Tab, pane: Pane) => void

const registry = new Map<string, Map<string, TabShortcutHandler>>()

export function registerTabShortcuts(
  kind: string,
  handlers: Record<string, TabShortcutHandler>,
): void {
  const existing = registry.get(kind) ?? new Map()
  for (const [action, handler] of Object.entries(handlers)) {
    existing.set(action, handler)
  }
  registry.set(kind, existing)
}

export function getTabShortcutHandler(
  kind: string,
  action: string,
): TabShortcutHandler | undefined {
  return registry.get(kind)?.get(action)
}

export function clearTabShortcutRegistry(): void {
  registry.clear()
}
