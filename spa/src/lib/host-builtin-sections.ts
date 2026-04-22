/**
 * host-builtin-sections.ts
 *
 * Built-in host sub-page adapter — registers six (or whatever the caller
 * supplies) `'host'`-scoped contributions under `moduleId = '_builtin.host'`.
 *
 * ## Design (post-#586)
 *
 * Built-ins are a **stable registration source**, not a one-shot pending
 * buffer.  `setHostBuiltinSections(defs[])` atomically replaces the full
 * source set; any localId absent from the new defs is dropped.
 * `dispatchSettingsContributions()` re-materializes host built-ins from the
 * source map on every call (no drain), so the dispatcher is idempotent: a
 * standalone second call to `dispatchSettingsContributions()` no longer
 * wipes built-in host contributions.
 *
 * ## Wrapper identity stability
 *
 * Each `localId`'s wrapper React component is created once
 * (`createHostBuiltinWrapper(localId)`) and reused across subsequent
 * `setHostBuiltinSections` calls — even when the inner section component
 * is a new reference (HMR reload of a section file).  The wrapper closes
 * over `localId` only; on render it reads the *current* component out of
 * the source map and delegates.  React sees the same wrapper component
 * type, so the host sub-page body does NOT remount across HMR.
 *
 * If a localId is dropped via `setHostBuiltinSections([])` and later re-
 * added, a fresh wrapper is created (the dropped wrapper is no longer
 * cached).
 *
 * ## HMR safety
 *
 * `clearHostBuiltinSources()` is exposed for the
 * `resetSettingsContributionsForHmr()` path.  Subsequent
 * `registerBuiltinModules()` runs repopulate via
 * `setHostBuiltinSections(...)` without duplication.
 */
import React from 'react'
import type {
  AnySettingsContributionDeclaration,
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
   * (the standard shape shared by all built-in host sections).  The
   * adapter wraps it with a scope guard so contributions receive the correct
   * `ctx` shape without modifying the section components.
   */
  component: React.ComponentType<{ hostId: string }>
}

// ----------------------------------------------------------------------------
// Stable source map
// ----------------------------------------------------------------------------

type HostCtxProps = { ctx: SettingsContextFor<'host'> }

interface HostBuiltinSource {
  // Mutable per-source — refreshed by setHostBuiltinSections().  The wrapper
  // reads these via the live map at render time so HMR-replacing the
  // underlying section component takes effect without changing the wrapper's
  // React component identity.
  component: React.ComponentType<{ hostId: string }>
  labelKey: string
  order: number
  // Stable per-localId — built once on first appearance and reused on every
  // subsequent setHostBuiltinSections call as long as the localId stays in
  // the source set.
  wrapped: React.ComponentType<HostCtxProps>
}

const hostBuiltinSources = new Map<string, HostBuiltinSource>()

/**
 * WeakMap from the ctx-wrapper component → the most recently registered
 * underlying section component.  Used by tests to verify identity / forwarding
 * without a full DOM render.  Updated in lockstep by `setHostBuiltinSections`
 * so a test reading from the WeakMap always sees the latest section.
 */
export const hostBuiltinComponentMap = new WeakMap<
  React.ComponentType<HostCtxProps>,
  React.ComponentType<{ hostId: string }>
>()

function createHostBuiltinWrapper(localId: string): React.ComponentType<HostCtxProps> {
  const Wrapped: React.FC<HostCtxProps> = (props) => {
    if (props.ctx.scope !== 'host') return null
    const source = hostBuiltinSources.get(localId)
    if (!source) return null
    const Section = source.component
    return React.createElement(Section, { hostId: props.ctx.hostId })
  }
  Wrapped.displayName = `HostBuiltinWrap(${localId})`
  return Wrapped
}

/**
 * Atomically replace the full set of built-in host sub-page sources.
 * Any localId present in the previous state but absent from `defs` is
 * dropped.  For each retained localId the cached wrapper is reused so its
 * React component identity is stable across calls — including HMR reloads
 * where `def.component` is a freshly imported reference.  The next
 * `dispatchSettingsContributions()` materializes exactly these.
 */
export function setHostBuiltinSections(defs: readonly HostBuiltinSectionDef[]): void {
  const nextIds = new Set<string>()
  for (const def of defs) nextIds.add(def.localId)

  // Drop localIds not in the new set.  Their cached wrappers are released —
  // a subsequent re-add for the same localId rebuilds the wrapper.
  for (const key of Array.from(hostBuiltinSources.keys())) {
    if (!nextIds.has(key)) hostBuiltinSources.delete(key)
  }

  // Upsert each def; reuse wrapper when present so identity is stable.
  for (const def of defs) {
    const existing = hostBuiltinSources.get(def.localId)
    const wrapped = existing?.wrapped ?? createHostBuiltinWrapper(def.localId)
    hostBuiltinSources.set(def.localId, {
      component: def.component,
      labelKey: def.labelKey,
      order: def.order,
      wrapped,
    })
    // Keep the WeakMap pointed at the latest section component so tests that
    // read identity see the freshest reference.
    hostBuiltinComponentMap.set(wrapped, def.component)
  }
}

/**
 * Snapshot the current built-in host source set as contribution declarations.
 * Called by the dispatcher's batch-build pass; the returned array's component
 * references are stable across calls when the source set is unchanged.
 */
export function getHostBuiltinDeclarations(): readonly AnySettingsContributionDeclaration[] {
  const out: AnySettingsContributionDeclaration[] = []
  for (const [localId, src] of hostBuiltinSources.entries()) {
    out.push({
      localId,
      scope: 'host',
      order: src.order,
      labelKey: src.labelKey,
      component: src.wrapped,
    })
  }
  return out
}

/**
 * HMR dispose hook + test reset.  Clears the stable source map so the next
 * `setHostBuiltinSections(...)` call starts from an empty set.
 */
export function clearHostBuiltinSources(): void {
  hostBuiltinSources.clear()
}
