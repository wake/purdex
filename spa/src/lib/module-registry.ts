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
  tabId?: string
  isActive: boolean
  region?: SidebarRegion
}

export interface ViewDefinition {
  id: string
  label: string
  icon: React.ComponentType<{ size?: number; className?: string }>
  scope: 'system' | 'workspace' | 'tab'
  component: React.ComponentType<ViewProps>
}

export interface ConfigDef {
  key: string
  type: 'string' | 'boolean' | 'number'
  label: string
  required?: boolean
  defaultValue?: unknown
}

export interface CommandContribution {
  id: string
  name: string
  command: string | ((ctx: CommandContext) => string)
  icon?: string
  category?: string
}

export interface CommandContext {
  hostId: string
  workspaceId?: string | null
  moduleConfig?: Record<string, unknown>
}

export interface ModuleDefinition {
  id: string
  name: string
  pane?: PaneDefinition
  views?: ViewDefinition[]
  workspaceConfig?: ConfigDef[]
  globalConfig?: ConfigDef[]
  commands?: CommandContribution[]
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

export function getAllViews(): ViewDefinition[] {
  return [...modules.values()].flatMap((m) => m.views ?? [])
}

export function getModulesWithWorkspaceConfig(): ModuleDefinition[] {
  return [...modules.values()].filter((m) => m.workspaceConfig && m.workspaceConfig.length > 0)
}

export function getModulesWithGlobalConfig(): ModuleDefinition[] {
  return [...modules.values()].filter((m) => m.globalConfig && m.globalConfig.length > 0)
}

export function getModulesWithCommands(): ModuleDefinition[] {
  return [...modules.values()].filter((m) => m.commands && m.commands.length > 0)
}

export function clearModuleRegistry(): void {
  modules.clear()
}
