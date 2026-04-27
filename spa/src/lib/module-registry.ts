import type React from 'react'
import type { Pane } from '../types/tab'
import type { SidebarRegion } from '../types/layout'
import type { AnySettingsContributionDeclaration } from './settings-contribution-types'

// Re-export for convenience
export type { SidebarRegion } from '../types/layout'
export type {
  AnySettingsContribution,
  AnySettingsContributionDeclaration,
  SettingsContribution,
  SettingsContributionDeclaration,
  SettingsContext,
  SettingsContextFor,
  SettingsScope,
} from './settings-contribution-types'

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
  panes?: PaneDefinition[]
  views?: ViewDefinition[]
  /** @deprecated Use `settings: [{ scope: 'workspace', localId }]` instead. Will be removed after the files module migrates. */
  workspaceConfig?: ConfigDef[]
  /** @deprecated Use `settings: [{ scope: 'purdex', localId }]` instead. Will be removed after all consumers migrate. */
  globalConfig?: ConfigDef[]
  commands?: CommandContribution[]
  settings?: AnySettingsContributionDeclaration[]
  /**
   * File openers contributed by this module. `applyModuleFileOpeners()`
   * registers them into the file-opener registry (with `ownerModuleId = m.id`)
   * and skips them when a `disableable` module is currently disabled.
   * Module owners declare openers here instead of calling `registerFileOpener`
   * directly so the registry stays in sync with module enable state.
   */
  fileOpeners?: import('./file-opener-registry').FileOpener[]
  /**
   * When `true`, the module appears in the Modules Switchboard and can be
   * disabled by the user. Default `false` — a safe, explicit opt-in: module
   * owners must deliberately mark a module as independent enough to be
   * toggled off. Core / system modules (sessions / settings / hosts / etc.)
   * omit this flag and can never be disabled.
   */
  disableable?: boolean
  /** i18n key for the row description rendered in the Modules Switchboard. */
  descriptionKey?: string
  /**
   * Optional custom component to render when a pane of this module is shown
   * but the module is disabled. If unset, PaneLayoutRenderer falls back to
   * the generic `DisabledModulePlaceholder`. Use only if the module needs a
   * domain-specific recovery affordance — not the default.
   *
   * NOTE: module-registry MUST NOT import this component. It is held only as
   * a type reference; the concrete component is registered by the module
   * owner inside register-modules/<module>.tsx.
   */
  disabledComponent?: React.ComponentType<{ moduleId: string; paneKind: string }>
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
    for (const p of m.panes ?? []) {
      if (p.kind === kind) return p
    }
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
