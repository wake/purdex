import type React from 'react'

export type SettingsScope = 'purdex' | 'host' | 'workspace'

// Discriminated union — scope field narrows hostId/workspaceId.
export type SettingsContext =
  | { scope: 'purdex' }
  | { scope: 'host'; hostId: string }
  | { scope: 'workspace'; workspaceId: string }

// Author-facing declaration. `id` and `moduleId` are system-filled by
// register pass, not written by authors.
export interface SettingsContributionDeclaration {
  localId: string                                          // unique within a module; [a-zA-Z][a-zA-Z0-9_-]*
  scope: SettingsScope
  order: number                                            // ascending sort within scope
  labelKey: string                                         // i18n key
  descriptionKey?: string                                  // i18n key (optional)
  component: React.ComponentType<{ ctx: SettingsContext }>
  disabled?: (ctx: SettingsContext) => boolean
  disabledReasonKey?: string
}

// Registry-stored / shell-consumed form. System-filled fields present.
export interface SettingsContribution extends SettingsContributionDeclaration {
  id: string       // `${moduleId}.${localId}` — globally unique within registry
  moduleId: string
}
