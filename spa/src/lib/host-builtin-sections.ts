/**
 * host-builtin-sections.ts
 *
 * Built-in host sub-page adapter — mirrors the PR-2 dispatch-flushed
 * pattern used by the legacy settings adapter (`settings-section-registry`),
 * but scoped to `'host'` contributions with `moduleId = '_builtin.host'`.
 *
 * ## Why a separate queue (not shared with the legacy adapter)
 *
 * The legacy adapter (`_builtin.legacy-section`) is purdex-scoped.  Sharing
 * the same pending buffer would conflate two independent namespaces and make
 * the collision-checking logic in `buildSettingsContributionBatch` harder to
 * reason about.  A parallel queue keeps each adapter's invariants isolated
 * and the drain/peek surface matches the legacy adapter's contract exactly
 * (same shape, same atomicity guarantees — PR-3 Finding F3).
 *
 * ## Dispatch contract (§7.2 hard constraint)
 *
 * `registerBuiltinHostSection()` MUST NOT call `registerSettingsContribution()`
 * directly.  It pushes to `pendingHostBuiltinContributions`, which is peeked
 * (non-destructively) during Phase 1 validation and drained atomically during
 * Phase 2 commit inside `dispatchSettingsContributions()`.  This prevents
 * `clearContributions()` from nuking built-ins registered before dispatch.
 *
 * ## HMR safety
 *
 * `registerBuiltinModules()` may re-run on hot-reload.  The buffer is drained
 * (cleared) after each successful dispatch, so re-runs produce a clean batch
 * without duplicates.  The `clearHostBuiltinPending()` helper is also exposed
 * for the `resetSettingsContributionsForHmr()` path.
 */
import React from 'react'
import type {
  AnySettingsContributionDeclaration,
  SettingsContributionDeclaration,
  SettingsContextFor,
} from './settings-contribution-types'

export const HOST_BUILTIN_MODULE_ID = '_builtin.host'

export interface HostBuiltinSectionDef {
  /** URL segment and registry localId (must match SETTINGS_LOCAL_ID_RE). */
  localId: string
  /** i18n key for sidebar label. */
  labelKey: string
  /** Position within host scope; lower = earlier. */
  order: number
  /**
   * The underlying section component.  Receives `{ hostId: string }` props
   * (the standard shape shared by all six built-in host sections).  The
   * adapter wraps it with a scope guard so contributions receive the correct
   * `ctx` shape without modifying the section components.
   */
  component: React.ComponentType<{ hostId: string }>
}

// ----------------------------------------------------------------------------
// Pending buffer (PR-2 dispatch-flushed pattern)
// ----------------------------------------------------------------------------

type HostBuiltinDeclaration = SettingsContributionDeclaration<'host'>
type HostCtxProps = { ctx: SettingsContextFor<'host'> }

/**
 * WeakMap from the ctx-wrapper component → the original section component.
 * Exposed for tests that verify `hostId` forwarding without a full DOM render.
 */
// Exported for test identity assertions only — see host-builtin-sections.test.tsx
export const hostBuiltinComponentMap = new WeakMap<
  React.ComponentType<HostCtxProps>,
  React.ComponentType<{ hostId: string }>
>()

// Upsert map by localId so HMR re-runs do not accumulate duplicates.
const pendingHostBuiltinContributions = new Map<string, HostBuiltinDeclaration>()

/**
 * Register a built-in host sub-page into the pending buffer.  The
 * contribution is NOT committed to the live registry here — it waits for the
 * next `dispatchSettingsContributions()` call to flush.
 */
export function registerBuiltinHostSection(def: HostBuiltinSectionDef): void {
  // Scope guard wrapper: forward `hostId` from ctx; return null for wrong scope.
  // Using React.createElement keeps this file as .ts (no JSX transform needed).
  const Section = def.component
  const Wrapped: React.FC<HostCtxProps> = (props) => {
    if (props.ctx.scope !== 'host') return null
    return React.createElement(Section, { hostId: props.ctx.hostId })
  }
  Wrapped.displayName = `HostBuiltinWrap(${Section.displayName ?? Section.name ?? def.localId})`

  hostBuiltinComponentMap.set(Wrapped, Section)

  const decl: HostBuiltinDeclaration = {
    localId: def.localId,
    scope: 'host',
    order: def.order,
    labelKey: def.labelKey,
    component: Wrapped,
  }
  pendingHostBuiltinContributions.set(def.localId, decl)
}

/**
 * Non-destructive view of the pending host built-in queue.  Used by the
 * dispatch validation phase (Phase 1) so a validation failure leaves the
 * queue intact for a retry.
 */
export function peekHostBuiltinQueue(): readonly AnySettingsContributionDeclaration[] {
  return Array.from(pendingHostBuiltinContributions.values())
}

/**
 * Drain pending host built-in contributions into a fresh array, emptying the
 * internal buffer.  F3: COMMIT-ONLY — call only after full validation has
 * succeeded (see `drainLegacyContributionQueue` docs for rationale).
 */
export function drainHostBuiltinQueue(): AnySettingsContributionDeclaration[] {
  const out: AnySettingsContributionDeclaration[] = Array.from(
    pendingHostBuiltinContributions.values(),
  )
  pendingHostBuiltinContributions.clear()
  return out
}

/**
 * HMR dispose hook.  Clears the active pending buffer so no stale entries
 * leak across HMR re-runs.
 */
export function clearHostBuiltinPending(): void {
  pendingHostBuiltinContributions.clear()
}
