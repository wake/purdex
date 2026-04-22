import { getModules, type ModuleDefinition } from './module-registry'
import {
  assertValidSettingsContribution,
  clearContributions,
  registerSettingsContribution,
} from './settings-contribution-registry'
import { drainLegacyContributionQueue } from './settings-section-registry'
import type {
  AnySettingsContribution,
  SettingsScope,
} from './settings-contribution-types'

// Legacy adapter namespace constant — sections registered through the legacy
// `registerSettingsSection()` API are flushed into the new registry under this
// moduleId. Centralized so PR-3/4 can reuse the same pattern.
const LEGACY_SECTION_MODULE_ID = '_builtin.legacy-section'

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
    origin: 'module' | 'legacy',
  ): void => {
    assertValidSettingsContribution(full)
    if (seenIds.has(full.id)) {
      throw new Error(
        origin === 'legacy'
          ? `settings-contribution-registry: duplicate contribution id "${full.id}" ` +
              `(legacy adapter collides with module-declared contribution)`
          : `settings-contribution-registry: duplicate contribution id "${full.id}"`,
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

  // Drain legacy adapter pending buffer. PR-2 wires the real pending buffer
  // through `registerSettingsSection()`. Each call pushes a declaration here
  // that is composed with module-declared contributions under the same shared
  // collision map (F1) in this single atomic pass.
  const legacyDecls = drainLegacyContributionQueue()
  for (const decl of legacyDecls) {
    const full = {
      ...decl,
      moduleId: LEGACY_SECTION_MODULE_ID,
      id: `${LEGACY_SECTION_MODULE_ID}.${decl.localId}`,
    } as AnySettingsContribution
    checkAndRecord(full, 'legacy')
    batch.push(full)
  }

  return batch
}

export function dispatchSettingsContributions(
  modules: ModuleDefinition[] = getModules(),
): void {
  const batch = buildSettingsContributionBatch(modules)
  clearContributions()
  for (const contribution of batch) {
    registerSettingsContribution(contribution)
  }
}
