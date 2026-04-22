/**
 * host-builtin-sections.ts
 *
 * Built-in host sub-page adapter — registers six (or whatever the caller
 * supplies) `'host'`-scoped contributions under `moduleId = '_builtin.host'`.
 *
 * ## Design (post-#586, with R2 attacker/defender/file-health A1 transactional fix)
 *
 * Built-ins live in two maps:
 *
 *   - `stagedSources` — what `setHostBuiltinSections(defs)` last said.
 *     Mutated immediately on every call.  Read ONLY by the dispatcher's
 *     batch-build pass via `getHostBuiltinDeclarations()`.
 *
 *   - `liveSources` — what was last successfully committed by
 *     `commitHostBuiltinSources()` (called from Phase 2 of
 *     `dispatchSettingsContributions()`).  Read by every wrapper at render
 *     time.
 *
 * `dispatchSettingsContributions()` is responsible for swapping staged→live
 * after Phase 1 validation succeeds.  If validation throws, `liveSources`
 * stays untouched — wrappers continue rendering the previously committed
 * built-ins, so registry metadata and rendered body stay in sync (no
 * split-brain on partial failure).
 *
 * `dispatchSettingsContributions()` is idempotent: a second standalone call
 * (no re-`setHostBuiltinSections`) re-validates the same staged set, then
 * re-commits the same liveSources content (no observable change).
 *
 * ## Wrapper identity stability
 *
 * Each `localId`'s wrapper React component is created once
 * (`createHostBuiltinWrapper(localId)`), cached in `wrapperCache`, and reused
 * across subsequent calls — even when the inner section component is a new
 * reference (HMR reload of a section file).  The wrapper closes over
 * `localId` only; on render it reads the *currently committed* component
 * out of `liveSources` and delegates.  React sees the same wrapper component
 * type, so the host sub-page body does NOT remount across HMR.
 *
 * Wrappers stay cached even after a `localId` is dropped from staged/live
 * (until `clearHostBuiltinSources()`), so a subsequent re-add reuses the
 * same wrapper reference — strictly stronger identity stability than the
 * pre-R2 design.
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
// Two-tier source map: staged (input) vs live (committed)
// ----------------------------------------------------------------------------

type HostCtxProps = { ctx: SettingsContextFor<'host'> }

interface HostBuiltinLiveSource {
  component: React.ComponentType<{ hostId: string }>
  labelKey: string
  order: number
}

const stagedSources = new Map<string, HostBuiltinSectionDef>()
const liveSources = new Map<string, HostBuiltinLiveSource>()
const wrapperCache = new Map<string, React.ComponentType<HostCtxProps>>()

/**
 * WeakMap from the ctx-wrapper component → the most recently *committed*
 * underlying section component.  Used by tests to verify identity / forwarding
 * without a full DOM render.  Updated only on `commitHostBuiltinSources()` —
 * staged-but-uncommitted changes are not visible here.
 */
export const hostBuiltinComponentMap = new WeakMap<
  React.ComponentType<HostCtxProps>,
  React.ComponentType<{ hostId: string }>
>()

function createHostBuiltinWrapper(localId: string): React.ComponentType<HostCtxProps> {
  const Wrapped: React.FC<HostCtxProps> = (props) => {
    if (props.ctx.scope !== 'host') return null
    // Read from LIVE (committed) state.  If a localId is in the wrapper
    // cache but not in liveSources (dropped without re-commit, or commit
    // has not yet happened), render null — the registry metadata likewise
    // lacks the entry, so the route layer self-heals away from this sub-
    // page rather than rendering stale content.
    const source = liveSources.get(localId)
    if (!source) return null
    const Section = source.component
    return React.createElement(Section, { hostId: props.ctx.hostId })
  }
  Wrapped.displayName = `HostBuiltinWrap(${localId})`
  return Wrapped
}

/**
 * Stage a new full set of built-in host sub-page sources.  Does NOT alter
 * the live (committed) state observed by wrappers; that swap happens inside
 * `commitHostBuiltinSources()` which the dispatcher calls after Phase 1
 * validation.  Wrappers are pre-created here so the dispatcher's batch-build
 * pass can reference them by stable identity.
 */
export function setHostBuiltinSections(defs: readonly HostBuiltinSectionDef[]): void {
  stagedSources.clear()
  for (const def of defs) {
    stagedSources.set(def.localId, def)
    if (!wrapperCache.has(def.localId)) {
      wrapperCache.set(def.localId, createHostBuiltinWrapper(def.localId))
    }
  }
}

/**
 * Snapshot the current staged set as contribution declarations for the
 * dispatcher to validate + commit.  Returns wrapper components from the
 * stable per-localId cache so identity is preserved across batch builds.
 *
 * @internal Used only by `dispatch-settings-contributions.ts`.
 */
export function getHostBuiltinDeclarations(): readonly AnySettingsContributionDeclaration[] {
  const out: AnySettingsContributionDeclaration[] = []
  for (const [localId, def] of stagedSources.entries()) {
    const wrapped = wrapperCache.get(localId)!
    out.push({
      localId,
      scope: 'host',
      order: def.order,
      labelKey: def.labelKey,
      component: wrapped,
    })
  }
  return out
}

/**
 * Commit the staged source map to live, becoming visible to wrappers.
 * Called from Phase 2 of `dispatchSettingsContributions()` ONLY after
 * Phase 1 validation has succeeded.  Fixes the R2 A1 finding: a failed
 * dispatch leaves `liveSources` (and therefore wrappers' render output)
 * untouched, so registry metadata and rendered body stay in sync.
 *
 * @internal Used only by `dispatch-settings-contributions.ts`.
 */
export function commitHostBuiltinSources(): void {
  // Drop live entries no longer in staged.
  for (const key of Array.from(liveSources.keys())) {
    if (!stagedSources.has(key)) liveSources.delete(key)
  }
  // Upsert staged into live.  Wrappers read live, so once we set here, the
  // next wrapper render sees the new component.  Wrapper identity itself is
  // unchanged (cached in wrapperCache by localId).
  for (const [localId, def] of stagedSources.entries()) {
    liveSources.set(localId, {
      component: def.component,
      labelKey: def.labelKey,
      order: def.order,
    })
    const wrapped = wrapperCache.get(localId)!
    hostBuiltinComponentMap.set(wrapped, def.component)
  }
}

/**
 * HMR dispose hook + test reset.  Clears every tier — staged, live, and the
 * wrapper cache — so the next `setHostBuiltinSections(...)` call starts from
 * a fully empty slate.
 */
export function clearHostBuiltinSources(): void {
  stagedSources.clear()
  liveSources.clear()
  wrapperCache.clear()
}
