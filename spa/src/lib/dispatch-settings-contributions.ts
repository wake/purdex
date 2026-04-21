import { getModules, type ModuleDefinition } from './module-registry'
import {
  assertValidSettingsContribution,
  clearContributions,
  registerSettingsContribution,
} from './settings-contribution-registry'
import { drainLegacyContributionQueue } from './settings-section-registry'
import type { AnySettingsContribution } from './settings-contribution-types'

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

export function buildSettingsContributionBatch(
  modules: ModuleDefinition[] = getModules(),
): AnySettingsContribution[] {
  const batch: AnySettingsContribution[] = []
  const seenIds = new Set<string>()

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
      assertValidSettingsContribution(full)
      if (seenIds.has(full.id)) {
        throw new Error(
          `settings-contribution-registry: duplicate contribution id "${full.id}"`,
        )
      }
      seenIds.add(full.id)
      batch.push(full)
    }
  }

  // Drain legacy adapter pending buffer. Commit 1 stub returns [] so behavior
  // matches main. Commit 2 wires the real pending buffer, at which point each
  // `registerSettingsSection()` call push declarations here that get composed
  // with module-declared contributions in the same atomic pass.
  const legacyDecls = drainLegacyContributionQueue()
  for (const decl of legacyDecls) {
    const full = {
      ...decl,
      moduleId: LEGACY_SECTION_MODULE_ID,
      id: `${LEGACY_SECTION_MODULE_ID}.${decl.localId}`,
    } as AnySettingsContribution
    assertValidSettingsContribution(full)
    if (seenIds.has(full.id)) {
      // Defensive: legacy moduleId is fixed to `_builtin.legacy-section`, so
      // collisions between module-declared and legacy ids imply an author
      // prefixed their own moduleId with `_builtin.legacy-section` — reject.
      throw new Error(
        `settings-contribution-registry: duplicate contribution id "${full.id}" ` +
          `(legacy adapter collides with module-declared contribution)`,
      )
    }
    seenIds.add(full.id)
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
