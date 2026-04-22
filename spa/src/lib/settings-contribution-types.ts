import type React from 'react'

export type SettingsScope = 'purdex' | 'host' | 'workspace'

/**
 * Canonical character grammar for a contribution's `localId`.
 *
 * Source of truth shared by:
 *   - `assertValidSettingsContribution()` in
 *     `./settings-contribution-registry.ts` (registration-time gate)
 *   - `SETTINGS_SECTION_PATTERN` consumer in `./route-utils.ts`
 *     (URL parser — re-exported here so parseRoute reuses the same regex)
 *
 * Keeping these in lockstep prevents a contribution that registers fine
 * under the registry but then silently fails `parseRoute()` on reload /
 * route-sync, which would redirect the user to the default section. See
 * PR-2 Round 3 finding F6.
 *
 * Grammar: lowercase ASCII, digits, or hyphen; 1–32 chars.
 */
export const SETTINGS_LOCAL_ID_RE = /^[a-z0-9-]{1,32}$/

// Discriminated union — scope field narrows hostId/workspaceId.
export type SettingsContext =
  | { scope: 'purdex' }
  | { scope: 'host'; hostId: string }
  | { scope: 'workspace'; workspaceId: string }

export type SettingsContextFor<S extends SettingsScope> = Extract<SettingsContext, { scope: S }>

// Author-facing declaration. `id` and `moduleId` are system-filled by
// register pass, not written by authors.
export interface SettingsContributionDeclaration<S extends SettingsScope = SettingsScope> {
  localId: string                                          // unique within a module; must match SETTINGS_LOCAL_ID_RE
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
