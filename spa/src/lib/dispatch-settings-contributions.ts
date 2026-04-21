import { getModules, type ModuleDefinition } from './module-registry'
import {
  assertValidSettingsContribution,
  clearContributions,
  registerSettingsContribution,
} from './settings-contribution-registry'
import type { SettingsContribution } from './settings-contribution-types'

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
): SettingsContribution[] {
  const batch: SettingsContribution[] = []
  const seenIds = new Set<string>()

  for (const module of modules) {
    const settings = module.settings
    if (!settings || settings.length === 0) continue

    assertNoLegacyScopeConflict(module)

    for (const decl of settings) {
      const full: SettingsContribution = {
        ...decl,
        moduleId: module.id,
        id: `${module.id}.${decl.localId}`,
      }
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
