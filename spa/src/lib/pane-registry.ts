import type React from 'react'
import type { Pane } from '../types/tab'

export interface PaneRendererProps {
  pane: Pane
  isActive: boolean
}

export interface PaneRendererConfig {
  component: React.ComponentType<PaneRendererProps>
}

const registry = new Map<string, PaneRendererConfig>()

export function registerPaneRenderer(kind: string, config: PaneRendererConfig): void {
  registry.set(kind, config)
}

export function getPaneRenderer(kind: string): PaneRendererConfig | undefined {
  return registry.get(kind)
}

export function clearPaneRegistry(): void {
  registry.clear()
}
