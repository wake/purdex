import type React from 'react'
import type { Pane, SidebarRegion } from '../types/tab'

// Re-export for convenience
export type { SidebarRegion } from '../types/tab'

// === Types ===

export interface PaneRendererProps {
  pane: Pane
  isActive: boolean
}

export interface PaneDefinition {
  kind: string
  component: React.ComponentType<PaneRendererProps>
}

export interface ViewProps {
  hostId?: string
  workspaceId?: string
  isActive: boolean
}

export interface ViewDefinition {
  id: string
  label: string
  icon: string
  scope: 'system' | 'workspace'
  defaultRegion: SidebarRegion
  component: React.ComponentType<ViewProps>
}

export interface ModuleDefinition {
  id: string
  name: string
  pane?: PaneDefinition
  views?: ViewDefinition[]
}

// === Registry ===

const modules = new Map<string, ModuleDefinition>()

export function registerModule(module: ModuleDefinition): void {
  modules.set(module.id, module)
}

export function unregisterModule(id: string): void {
  modules.delete(id)
}

export function getModule(id: string): ModuleDefinition | undefined {
  return modules.get(id)
}

export function getModules(): ModuleDefinition[] {
  return [...modules.values()]
}

// === Convenience queries ===

export function getPaneRenderer(kind: string): PaneDefinition | undefined {
  for (const m of modules.values()) {
    if (m.pane?.kind === kind) return m.pane
  }
  return undefined
}

export function getViewDefinition(viewId: string): ViewDefinition | undefined {
  for (const m of modules.values()) {
    if (!m.views) continue
    const view = m.views.find((v) => v.id === viewId)
    if (view) return view
  }
  return undefined
}

export function getViewsByRegion(
  region: SidebarRegion,
  scope?: 'system' | 'workspace',
): ViewDefinition[] {
  const result: ViewDefinition[] = []
  for (const m of modules.values()) {
    if (!m.views) continue
    for (const v of m.views) {
      if (v.defaultRegion === region && (!scope || v.scope === scope)) {
        result.push(v)
      }
    }
  }
  return result
}

export function clearModuleRegistry(): void {
  modules.clear()
}
