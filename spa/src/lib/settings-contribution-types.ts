import type React from 'react'

export type SettingsScope = 'purdex' | 'host' | 'workspace'

// Discriminated union — scope field narrows hostId/workspaceId.
export type SettingsContext =
  | { scope: 'purdex' }
  | { scope: 'host'; hostId: string }
  | { scope: 'workspace'; workspaceId: string }

export type SettingsContextFor<S extends SettingsScope> = Extract<SettingsContext, { scope: S }>

// Author-facing declaration. `id` and `moduleId` are system-filled by
// register pass, not written by authors.
export interface SettingsContributionDeclaration<S extends SettingsScope = SettingsScope> {
  localId: string                                          // unique within a module; [a-zA-Z][a-zA-Z0-9_-]*
  scope: S
  order: number                                            // ascending sort within scope
  labelKey: string                                         // i18n key
  descriptionKey?: string                                  // i18n key (optional)
  component: React.ComponentType<{ ctx: SettingsContextFor<S> }>
  disabled?: (ctx: SettingsContextFor<S>) => boolean
  disabledReasonKey?: string
}

// Registry-stored / shell-consumed form. System-filled fields present.
export interface SettingsContribution<S extends SettingsScope = SettingsScope>
  extends SettingsContributionDeclaration<S> {
  id: string       // `${moduleId}.${localId}` — globally unique within registry
  moduleId: string
}

// Distributive union — each element stays bound to its own scope's ctx type.
// Using `SettingsContributionDeclaration<SettingsScope>[]` at a container
// boundary would instantiate the generic once against the full union and
// erase the per-item scope↔ctx relationship, letting wrong-ctx components
// pass type-checking.
export type AnySettingsContributionDeclaration = {
  [S in SettingsScope]: SettingsContributionDeclaration<S>
}[SettingsScope]

export type AnySettingsContribution = {
  [S in SettingsScope]: SettingsContribution<S>
}[SettingsScope]
