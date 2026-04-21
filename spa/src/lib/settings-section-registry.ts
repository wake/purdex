import React from 'react'
import type {
  AnySettingsContributionDeclaration,
  SettingsContextFor,
  SettingsContribution,
  SettingsContributionDeclaration,
} from './settings-contribution-types'
import { listContributions } from './settings-contribution-registry'

// Legacy sections are always purdex-scoped — this is the ctx they receive.
type LegacyCtxProps = { ctx: SettingsContextFor<'purdex'> }

export interface SettingsSectionDef {
  id: string
  label: string
  order: number
  component?: React.ComponentType
}

// Legacy adapter moduleId. MUST match the constant used by the dispatch
// composer in `dispatch-settings-contributions.ts` — kept stringly-identical
// for the filter lookup in `getSettingsSections()` below. PR-3/4 will export
// this from a shared location if they need to reuse it; today two callers
// ship the same literal, guarded by these tests.
const LEGACY_SECTION_MODULE_ID = '_builtin.legacy-section'

// ----------------------------------------------------------------------------
// Legacy adapter state (PR-2 §7.2 dispatch-flushed pattern)
//
// - `pendingLegacyContributions` — active declarations waiting for the next
//   `dispatchSettingsContributions()` pass. Upsert by localId so HMR re-runs
//   do not accumulate duplicates. Drained atomically by dispatch.
// - `pendingReservedItems` — reserved sections (component: undefined). Never
//   flow into the new registry; kept as a separate Map so the sidebar can
//   still render coming-soon rows. Upsert by id for HMR parity.
// - `legacyComponentMap` — reverse lookup from the ctx-wrapped component we
//   hand the new registry back to the original author-supplied component, so
//   `getSettingsSections()` preserves React identity (important for memo).
// ----------------------------------------------------------------------------

type LegacyContributionDeclaration = SettingsContributionDeclaration<'purdex'>

const pendingLegacyContributions = new Map<string, LegacyContributionDeclaration>()
const pendingReservedItems = new Map<string, SettingsSectionDef>()
const legacyComponentMap = new WeakMap<
  React.ComponentType<LegacyCtxProps>,
  React.ComponentType
>()

function wrapLegacyComponent(
  Comp: React.ComponentType,
): React.ComponentType<LegacyCtxProps> {
  // Using React.createElement keeps this module .ts (no JSX). Legacy sections
  // were written before SettingsContext existed and do not read ctx —
  // intentionally discard props so existing section signatures stay
  // untouched (decision 2b — no code changes to existing sections).
  const Wrapped: React.ComponentType<LegacyCtxProps> = () =>
    React.createElement(Comp)
  Wrapped.displayName = `LegacyWrap(${Comp.displayName ?? Comp.name ?? 'Component'})`
  legacyComponentMap.set(Wrapped, Comp)
  return Wrapped
}

export function registerSettingsSection(def: SettingsSectionDef): void {
  if (def.component === undefined) {
    // Reserved — never flow into the new registry.
    pendingReservedItems.set(def.id, def)
    return
  }

  const wrapped = wrapLegacyComponent(def.component)
  const decl: LegacyContributionDeclaration = {
    localId: def.id,
    scope: 'purdex',
    order: def.order,
    labelKey: def.label,
    component: wrapped,
  }
  // Upsert by localId (N1 — active also not accumulating across HMR).
  pendingLegacyContributions.set(def.id, decl)
}

/**
 * Flattens the current registry view:
 *   - active entries read from the new contribution registry, filtered to
 *     legacy moduleId, restored to legacy shape via `legacyComponentMap`
 *   - reserved entries read from `pendingReservedItems`
 * merged and sorted by `order` ascending.
 *
 * Retained as a transitional read API for callsites not yet migrated to
 * `listContributions('purdex') + listReservedItems()` directly.
 */
export function getSettingsSections(): SettingsSectionDef[] {
  const active: SettingsSectionDef[] = []
  for (const c of listContributions('purdex')) {
    if (c.moduleId !== LEGACY_SECTION_MODULE_ID) continue
    active.push(toLegacyShape(c as SettingsContribution<'purdex'>))
  }
  const reserved = Array.from(pendingReservedItems.values())
  return [...active, ...reserved].sort((a, b) => a.order - b.order)
}

function toLegacyShape(c: SettingsContribution<'purdex'>): SettingsSectionDef {
  // The registered component is the ctx-taking wrapper; recover the original
  // for React identity preservation.
  const original = legacyComponentMap.get(c.component)
  return {
    id: c.localId,
    label: c.labelKey,
    order: c.order,
    component: original,
  }
}

export function clearSettingsSectionRegistry(): void {
  pendingLegacyContributions.clear()
  pendingReservedItems.clear()
}

/**
 * Drain pending legacy contributions into a fresh array, emptying the
 * internal buffer. Invoked by `dispatchSettingsContributions()` during the
 * flush pass. Returns a shallow copy — callers get full ownership.
 */
export function drainLegacyContributionQueue(): AnySettingsContributionDeclaration[] {
  const out: AnySettingsContributionDeclaration[] = Array.from(
    pendingLegacyContributions.values(),
  )
  pendingLegacyContributions.clear()
  return out
}

/**
 * Read-only snapshot of reserved sections, sorted by `order` ascending.
 * Consumed by `SettingsSidebar` to render coming-soon entries alongside
 * active contributions from `listContributions('purdex')`.
 */
export function listReservedItems(): readonly SettingsSectionDef[] {
  return Array.from(pendingReservedItems.values()).sort((a, b) => a.order - b.order)
}

/**
 * HMR dispose hook. Clears BOTH the active pending buffer and the reserved
 * map — required by N1 to prevent orphan reserved keys from leaking across
 * HMR boundaries (the reserved map uses upsert-by-id, but a rename of a
 * reserved section would leave its old key behind without this sweep).
 */
export function clearLegacyPending(): void {
  pendingLegacyContributions.clear()
  pendingReservedItems.clear()
}
