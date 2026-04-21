import type { AnySettingsContributionDeclaration } from './settings-contribution-types'

export interface SettingsSectionDef {
  id: string
  label: string
  order: number
  component?: React.ComponentType
}

const sections: SettingsSectionDef[] = []

export function registerSettingsSection(def: SettingsSectionDef): void {
  const idx = sections.findIndex((s) => s.id === def.id)
  if (idx >= 0) {
    sections[idx] = def
  } else {
    sections.push(def)
  }
  sections.sort((a, b) => a.order - b.order)
}

export function getSettingsSections(): SettingsSectionDef[] {
  return [...sections]
}

export function clearSettingsSectionRegistry(): void {
  sections.length = 0
}

// ----------------------------------------------------------------------------
// Legacy contribution adapter — stub (commit 1)
//
// Commit 2 will replace these stubs with real pending-buffer + reserved-map
// semantics described in the PR-2 plan §2 and §3.1. For commit 1 the stubs
// return empty so dispatch() sees no legacy declarations, preserving end-to-
// end behavior identical to `main`. Only the export surface exists in this
// commit so `dispatch-settings-contributions.ts` can reference the drain API
// and land independently-green.
// ----------------------------------------------------------------------------

/**
 * Drain the pending legacy contribution queue. Stub returns an empty array;
 * commit 2 wires up the real buffer.
 */
export function drainLegacyContributionQueue(): AnySettingsContributionDeclaration[] {
  return []
}

/**
 * Snapshot of reserved-only sections (component: undefined). Stub returns
 * an empty array; commit 2 wires up the reserved Map.
 */
export function listReservedItems(): readonly SettingsSectionDef[] {
  return []
}

/**
 * HMR dispose hook — clear both legacy pending buffer and reserved map.
 * Stub is a no-op; commit 2 wires up both.
 */
export function clearLegacyPending(): void {
  // no-op stub
}
