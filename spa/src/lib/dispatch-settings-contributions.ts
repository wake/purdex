import { getModules, type ModuleDefinition } from './module-registry'
import {
  assertValidSettingsContribution,
  clearContributions,
  registerSettingsContribution,
} from './settings-contribution-registry'
import {
  clearLegacyPending,
  drainLegacyContributionQueue,
  peekLegacyContributionQueue,
} from './settings-section-registry'
import {
  clearHostBuiltinSources,
  commitHostBuiltinSources,
  getHostBuiltinDeclarations,
  HOST_BUILTIN_MODULE_ID,
} from './host-builtin-sections'
import type {
  AnySettingsContribution,
  SettingsScope,
} from './settings-contribution-types'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'

// Legacy adapter namespace constant — sections registered through the legacy
// `registerSettingsSection()` API are flushed into the new registry under this
// moduleId. Centralized so PR-3/4 can reuse the same pattern.
const LEGACY_SECTION_MODULE_ID = '_builtin.legacy-section'

// PR-5 deprecation: module authors using `globalConfig` / `workspaceConfig`
// should migrate to `settings: [{ scope, localId }]`. `files` is exempt until
// the files owner completes its refactor.
const DEPRECATED_LEGACY_CONFIG_EXEMPT: ReadonlySet<string> = new Set(['files'])
const warnedDeprecationKeys = new Set<string>()

function warnLegacyConfigDeprecations(modules: readonly ModuleDefinition[]): void {
  for (const m of modules) {
    if (DEPRECATED_LEGACY_CONFIG_EXEMPT.has(m.id)) continue
    if (m.globalConfig && m.globalConfig.length > 0) {
      const key = `${m.id}:global`
      if (!warnedDeprecationKeys.has(key)) {
        warnedDeprecationKeys.add(key)
        console.warn(
          `[module] ${m.id} uses deprecated globalConfig; migrate to settings: [{ scope: 'purdex', localId }]`,
        )
      }
    }
    if (m.workspaceConfig && m.workspaceConfig.length > 0) {
      const key = `${m.id}:workspace`
      if (!warnedDeprecationKeys.has(key)) {
        warnedDeprecationKeys.add(key)
        console.warn(
          `[module] ${m.id} uses deprecated workspaceConfig; migrate to settings: [{ scope: 'workspace', localId }]`,
        )
      }
    }
  }
}

// Test-only reset — exposed so dedupe state doesn't leak across test cases.
export function resetDeprecationWarningsForTest(): void {
  warnedDeprecationKeys.clear()
}

function assertNoLegacyScopeConflict(module: ModuleDefinition): void {
  const settings = module.settings
  if (!settings || settings.length === 0) return

  if (module.globalConfig && module.globalConfig.length > 0) {
    const hasPurdexContribution = settings.some((item) => item.scope === 'purdex')
    if (hasPurdexContribution) {
      throw new Error(
        `Invariant I1 violated: module "${module.id}" exposes purdex scope via both ` +
          `globalConfig (legacy) and settings[scope='purdex']. Pick one.`,
      )
    }
  }

  if (module.workspaceConfig && module.workspaceConfig.length > 0) {
    const hasWorkspaceContribution = settings.some((item) => item.scope === 'workspace')
    if (hasWorkspaceContribution) {
      throw new Error(
        `Invariant I1 violated: module "${module.id}" exposes workspace scope via both ` +
          `workspaceConfig (legacy) and settings[scope='workspace']. Pick one.`,
      )
    }
  }
}

/**
 * Build the validated contribution batch for a dispatch pass. Runs two
 * collision checks in a single shared pass that spans BOTH module-declared
 * contributions AND the legacy adapter's pending queue:
 *
 *  1. Full-id uniqueness (`${moduleId}.${localId}`) — registry-level.
 *  2. Per-scope localId uniqueness (F1) — shell-level. Within a scope,
 *     `localId` must be unique across all modules. The shell uses `localId`
 *     as the URL segment and React key for SettingsPage routing/selection,
 *     so two contributions sharing a localId within the same scope are
 *     ambiguous at the UI layer (even though their full ids differ). The
 *     per-scope uniqueness contract lets the shell continue to use
 *     `localId` — URLs and selection state stay stable.
 *
 * On any validation failure, nothing is committed to the live registry —
 * `dispatchSettingsContributions()` only writes after `buildSettingsContributionBatch`
 * returns without throwing.
 */
export function buildSettingsContributionBatch(
  modules: ModuleDefinition[] = getModules(),
): AnySettingsContribution[] {
  const batch: AnySettingsContribution[] = []
  const seenIds = new Set<string>()
  // Shared across module-declared and legacy sources so collisions across
  // both layers are caught in a single pass.
  const localIdByScope = new Map<SettingsScope, Map<string, string>>()

  const checkAndRecord = (
    full: AnySettingsContribution,
    origin: 'module' | 'legacy' | 'host-builtin',
  ): void => {
    assertValidSettingsContribution(full)
    if (seenIds.has(full.id)) {
      const suffix =
        origin === 'legacy'
          ? ` (legacy adapter collides with module-declared contribution)`
          : origin === 'host-builtin'
            ? ` (host-builtin adapter collides with module-declared contribution)`
            : ``
      throw new Error(
        `settings-contribution-registry: duplicate contribution id "${full.id}"${suffix}`,
      )
    }
    let scopeMap = localIdByScope.get(full.scope)
    if (!scopeMap) {
      scopeMap = new Map()
      localIdByScope.set(full.scope, scopeMap)
    }
    const existingSource = scopeMap.get(full.localId)
    if (existingSource !== undefined) {
      throw new Error(
        `settings-contribution-registry: duplicate localId "${full.localId}" ` +
          `in scope "${full.scope}": already registered by "${existingSource}", ` +
          `now re-registered by "${full.id}". Within a scope, localId must be ` +
          `unique across modules.`,
      )
    }
    scopeMap.set(full.localId, full.id)
    seenIds.add(full.id)
  }

  for (const module of modules) {
    // Modules Switchboard: disabled modules skip their entire `settings: [...]`
    // block (all scopes). Note the filter precedes the localId/collision
    // checks, so a disabled module's localIds cannot clash with an enabled
    // module's (spec EC-3).
    if (!useModuleEnabledStore.getState().isEnabled(module.id)) continue

    const settings = module.settings
    if (!settings || settings.length === 0) continue

    assertNoLegacyScopeConflict(module)

    for (const decl of settings) {
      const full = {
        ...decl,
        moduleId: module.id,
        id: `${module.id}.${decl.localId}`,
      } as AnySettingsContribution
      checkAndRecord(full, 'module')
      batch.push(full)
    }
  }

  // Peek (non-destructive) at the legacy adapter pending buffer — F3. The
  // real drain happens only in `dispatchSettingsContributions()` AFTER full
  // validation of both sources has succeeded. A validation failure here
  // leaves the legacy queue intact, so after the author fixes the offending
  // declaration the next dispatch can re-validate and commit the
  // still-queued legacy sections without requiring re-registration.
  const legacyDecls = peekLegacyContributionQueue()
  for (const decl of legacyDecls) {
    const full = {
      ...decl,
      moduleId: LEGACY_SECTION_MODULE_ID,
      id: `${LEGACY_SECTION_MODULE_ID}.${decl.localId}`,
    } as AnySettingsContribution
    checkAndRecord(full, 'legacy')
    batch.push(full)
  }

  // Re-materialize host built-ins from the stable source map every dispatch.
  // The source map is long-lived (mutated only by setHostBuiltinSections /
  // clearHostBuiltinSources), so this is idempotent across repeated dispatch
  // calls — fixes #586 where a standalone second dispatch call would wipe
  // built-in host contributions because the old pending-buffer/drain
  // pattern emptied the buffer after the first commit.
  const hostBuiltinDecls = getHostBuiltinDeclarations()
  for (const decl of hostBuiltinDecls) {
    const full = {
      ...decl,
      moduleId: HOST_BUILTIN_MODULE_ID,
      id: `${HOST_BUILTIN_MODULE_ID}.${decl.localId}`,
    } as AnySettingsContribution
    checkAndRecord(full, 'host-builtin')
    batch.push(full)
  }

  return batch
}

/**
 * Atomic dispatch of all settings contributions. Phase 1 validates the full
 * batch (module-declared + peeked legacy queue) without mutating state.
 * Only if Phase 1 succeeds does Phase 2 commit: clear the live registry,
 * drain the legacy queue, and write the validated batch.
 *
 * Ordering within Phase 2 preserves the R1 Finding #1 atomic-two-phase
 * property: clear first, then register — the registry never observes a
 * mixed old/new state.
 */
export function dispatchSettingsContributions(
  modules: ModuleDefinition[] = getModules(),
): void {
  // Phase 1 — validate without mutating. Throws on any collision (F1) or
  // invariant violation. The legacy queue is peeked, not drained.
  const batch = buildSettingsContributionBatch(modules)

  // Phase 2 — commit. Safe to mutate now: validation passed.
  clearContributions()
  drainLegacyContributionQueue()  // safe: staging already holds the validated set
  // R2 attacker/defender/file-health A1 — host built-ins live in a staged map
  // that wrappers do NOT observe.  commitHostBuiltinSources() swaps staged
  // into the live map only here, in lock-step with the registry write below,
  // so a failed Phase 1 leaves both layers untouched (no split-brain between
  // sidebar/route metadata and the rendered body).
  commitHostBuiltinSources()
  for (const contribution of batch) {
    registerSettingsContribution(contribution)
  }

  // PR-5 deprecation warnings: emit only after a successful dispatch so a
  // validation-failed pass doesn't silently burn the dedupe key for the
  // successful retry.
  warnLegacyConfigDeprecations(modules)
}

/**
 * HMR dispose helper — clears BOTH the committed contribution registry
 * AND the legacy adapter's pending buffers, so the next module
 * registration starts from a clean slate across HMR re-runs (F2).
 *
 * Consolidated here (rather than calling the two write APIs directly from
 * `register-modules.tsx`) so the ESLint `no-restricted-imports` rule (F4)
 * can keep `clearContributions` off the public surface. This is the
 * canonical single entry point for HMR cleanup of settings contribution
 * state.
 */
export function resetSettingsContributionsForHmr(): void {
  clearContributions()
  clearLegacyPending()
  clearHostBuiltinSources()
  // R2 codex: also clear the deprecation-warn dedupe set. Without this, a
  // module author who temporarily removed and then re-added a legacy
  // `globalConfig` / `workspaceConfig` declaration during an HMR session
  // would never see the warning again — the dedupe key persists across
  // reloads even though the rest of the registry is freshly rebuilt.
  warnedDeprecationKeys.clear()
}
