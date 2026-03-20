import type { Tab } from '../types/tab'

export interface TabRendererProps {
  tab: Tab
  isActive: boolean
  wsBase: string
  daemonBase: string
}

export interface TabRendererConfig {
  component: React.ComponentType<TabRendererProps>
  viewModes?: string[]
  defaultViewMode?: string
  icon: (tab: Tab) => string
}

const registry = new Map<string, TabRendererConfig>()

export function registerTabRenderer(type: string, config: TabRendererConfig): void {
  registry.set(type, config)
}

export function getTabRenderer(type: string): TabRendererConfig | undefined {
  return registry.get(type)
}

export function getTabIcon(tab: Tab): string {
  const config = registry.get(tab.type)
  if (config) return config.icon(tab)
  return tab.icon
}

export function clearRegistry(): void {
  registry.clear()
}
