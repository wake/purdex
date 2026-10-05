import type React from 'react'
import type { Pane } from '../types/tab'
import type { AnySettingsContributionDeclaration } from './settings-contribution-types'

// Re-export for convenience
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
  /** The pane's tab is the one on screen. */
  isActive: boolean
  /**
   * This pane is its tab's focus target (rule F, shell cleanup spec §8.2): the most recently focused live pane of the
   * tab, else its primary pane. Independent of `isActive`. A renderer that focuses itself programmatically does so only
   * at activation (`isActive` false→true, or mounting active) and only when this is true — see `useActivationFocus`.
   * A change of this prop while `isActive` stays true never calls `focus()`.
   *
   * `PaneLayoutRenderer` always sets it. It is optional only so renderers mounted by hand (unit tests) keep compiling;
   * a renderer reads an absent value as `false` (destructure with `isFocusTarget = false`).
   */
  isFocusTarget?: boolean
}

export interface PaneDefinition {
  kind: string
  component: React.ComponentType<PaneRendererProps>
}

export interface ConfigDef {
  key: string
  type: 'string' | 'boolean' | 'number'
  label: string
  required?: boolean
  defaultValue?: unknown
}

export interface ModuleDefinition {
  id: string
  name: string
  panes?: PaneDefinition[]
  /** @deprecated Use `settings: [{ scope: 'workspace', localId }]` instead. Will be removed after the files module migrates. */
  workspaceConfig?: ConfigDef[]
  /** @deprecated Use `settings: [{ scope: 'purdex', localId }]` instead. Will be removed after all consumers migrate. */
  globalConfig?: ConfigDef[]
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

/**
 * Discriminated metadata returned by `resolvePaneRenderer()`. Holds component
 * references but never imports concrete UI components — the consumer (e.g.
 * `PaneLayoutRenderer`) is responsible for falling back to its own
 * `DisabledModulePlaceholder` when a `disabled` resolution surfaces without a
 * `customComponent`. Keeping the lib → UI direction one-way is critical:
 * `module-registry` must remain a leaf of the import graph.
 */
export type RendererResolution =
  | { kind: 'render'; component: React.ComponentType<PaneRendererProps> }
  | {
      kind: 'disabled'
      moduleId: string
      paneKind: string
      customComponent?: React.ComponentType<{ moduleId: string; paneKind: string }>
    }
  | { kind: 'unknown'; paneKind: string }

/**
 * Look up the pane definition for `paneKind` and return what the consumer
 * should render: the actual component, a placeholder hint when the owning
 * module is disabled, or an unknown-kind result.
 *
 * `isEnabled` is injected to avoid a circular `module-registry` ↔
 * `useModuleEnabledStore` import (the store already calls `getModule` to
 * decide whether an override applies). Pass
 * `useModuleEnabledStore.getState().isEnabled` from the render layer; the
 * default of `() => true` keeps unit tests free of store setup.
 */
export function resolvePaneRenderer(
  paneKind: string,
  isEnabled: (moduleId: string) => boolean = () => true,
): RendererResolution {
  for (const m of modules.values()) {
    for (const p of m.panes ?? []) {
      if (p.kind !== paneKind) continue
      if (m.disableable && !isEnabled(m.id)) {
        return {
          kind: 'disabled',
          moduleId: m.id,
          paneKind,
          customComponent: m.disabledComponent,
        }
      }
      return { kind: 'render', component: p.component }
    }
  }
  return { kind: 'unknown', paneKind }
}

export function getModulesWithWorkspaceConfig(): ModuleDefinition[] {
  return [...modules.values()].filter((m) => m.workspaceConfig && m.workspaceConfig.length > 0)
}

export function getModulesWithGlobalConfig(): ModuleDefinition[] {
  return [...modules.values()].filter((m) => m.globalConfig && m.globalConfig.length > 0)
}

export function clearModuleRegistry(): void {
  modules.clear()
}
