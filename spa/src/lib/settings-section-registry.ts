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
  component: React.ComponentType
}

// Legacy adapter moduleId. MUST match the constant used by the dispatch
// composer in `dispatch-settings-contributions.ts` — kept stringly-identical
// for the filter lookup in `getSettingsSections()` below. PR-3/4 will export
// this from a shared location if they need to reuse it; today two callers
// ship the same literal, guarded by these tests.
const LEGACY_SECTION_MODULE_ID = '_builtin.legacy-section'

// ----------------------------------------------------------------------------
// Legacy adapter state (PR-2 §7.2 dispatch-flushed pattern, PR-3 trimmed)
//
// - `pendingLegacyContributions` — active declarations waiting for the next
//   `dispatchSettingsContributions()` pass. Upsert by localId so HMR re-runs
//   do not accumulate duplicates. Drained atomically by dispatch.
// - `legacyComponentMap` — reverse lookup from the ctx-wrapped component we
//   hand the new registry back to the original author-supplied component, so
//   `getSettingsSections()` preserves React identity (important for memo).
//
// Reserved (`component: undefined`) plumbing was dropped in PR-3 — the sole
// reserved entry (`workspace`) was removed alongside the removal. F5 follow-up
// softened the missing-component handling from `throw` to `console.warn +
// early return` so a stale caller (HMR leftover, version-mismatched dev
// snippet) no longer crashes the whole shell at bootstrap.
// ----------------------------------------------------------------------------

type LegacyContributionDeclaration = SettingsContributionDeclaration<'purdex'>

const pendingLegacyContributions = new Map<string, LegacyContributionDeclaration>()
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
  // F5: reserved semantics (`component: undefined`) have been removed, but
  // throwing here previously crashed `registerBuiltinModules()` at bootstrap
  // whenever any stale caller (dev-console snippet, HMR leftover, parallel-
  // session version mismatch) hit the function without a component — which
  // killed the whole shell. Soft-fail: log a clear warning and skip the
  // registration so subsequent calls complete.
  if (def.component === undefined) {
    console.warn(
      `[settings-section-registry] registerSettingsSection called with ` +
        `component: undefined for id="${def.id}". Reserved-row semantics were ` +
        `removed in HSR PR-3; this registration is ignored. If you intended to ` +
        `declare a disabled contribution, use ModuleDefinition.settings with ` +
        `disabled(ctx) returning true instead.`,
    )
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
  // Upsert by localId (N1 — active not accumulating across HMR).
  pendingLegacyContributions.set(def.id, decl)
}

/**
 * Flattens the current registry view: active entries read from the new
 * contribution registry, filtered to legacy moduleId, restored to legacy
 * shape via `legacyComponentMap`; sorted by `order` ascending.
 *
 * Retained as a transitional read API for tests that inspect the legacy
 * adapter pathway directly (no production callers remain). Reserved rows
 * are no longer part of this shape — see PR-3.
 */
export function getSettingsSections(): SettingsSectionDef[] {
  const active: SettingsSectionDef[] = []
  for (const c of listContributions('purdex')) {
    if (c.moduleId !== LEGACY_SECTION_MODULE_ID) continue
    active.push(toLegacyShape(c as SettingsContribution<'purdex'>))
  }
  return active.sort((a, b) => a.order - b.order)
}

function toLegacyShape(c: SettingsContribution<'purdex'>): SettingsSectionDef {
  // The registered component is the ctx-taking wrapper; recover the original
  // for React identity preservation.
  const original = legacyComponentMap.get(c.component)
  // PR-3 invariant: registerSettingsSection now refuses component-undefined,
  // so every wrapped component in the registry maps back to a real original.
  // Fall back to the wrapper defensively — callers inspect identity at most.
  return {
    id: c.localId,
    label: c.labelKey,
    order: c.order,
    component: original ?? (c.component as unknown as React.ComponentType),
  }
}

export function clearSettingsSectionRegistry(): void {
  pendingLegacyContributions.clear()
}

/**
 * Drain pending legacy contributions into a fresh array, emptying the
 * internal buffer. Returns a shallow copy — callers get full ownership.
 *
 * F3: COMMIT-ONLY. Only call this after full validation of the batch has
 * succeeded — otherwise a validation failure downstream leaves the queue
 * empty AND the new registry un-updated, so a retry (after the author
 * fixes the offending declaration) silently drops every previously-queued
 * legacy section because the drain already happened. Use
 * `peekLegacyContributionQueue()` for non-destructive validation reads.
 *
 * The current caller,
 * {@link dispatch-settings-contributions!buildSettingsContributionBatch},
 * peeks first, runs full validation on both sources, and only then calls
 * this function as part of the commit phase.
 */
export function drainLegacyContributionQueue(): AnySettingsContributionDeclaration[] {
  const out: AnySettingsContributionDeclaration[] = Array.from(
    pendingLegacyContributions.values(),
  )
  pendingLegacyContributions.clear()
  return out
}

/**
 * Non-destructive view of the pending legacy contribution queue. Returns a
 * shallow copy — callers may iterate freely but must not mutate the inner
 * declarations. Used by the dispatch validation path (F3) to run collision
 * checks without clearing state, so a thrown validation error leaves the
 * queue intact for a retry after the author fixes the offending
 * declaration.
 */
export function peekLegacyContributionQueue(): readonly AnySettingsContributionDeclaration[] {
  return Array.from(pendingLegacyContributions.values())
}

/**
 * HMR dispose hook. Clears the active pending buffer. Reserved-map clearing
 * was dropped in PR-3 alongside the reserved-items feature itself.
 */
export function clearLegacyPending(): void {
  pendingLegacyContributions.clear()
}
