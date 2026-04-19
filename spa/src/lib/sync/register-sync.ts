import { createSyncEngine } from './engine'
import { createPreferencesContributor } from './contributors/preferences'
import { createWorkspacesContributor } from './contributors/workspaces'
import { createHostsContributor } from './contributors/hosts'
import { createLayoutContributor } from './contributors/layout'
import { createQuickCommandsContributor } from './contributors/quick-commands'
import { createI18nContributor } from './contributors/i18n'
import { createNotificationSettingsContributor } from './contributors/notification-settings'
import { setAllContributorIds } from './use-sync-store'

export const syncEngine = createSyncEngine()

export function registerSyncContributors(): void {
  syncEngine.register(createPreferencesContributor())
  syncEngine.register(createWorkspacesContributor())
  syncEngine.register(createHostsContributor())
  syncEngine.register(createLayoutContributor())
  syncEngine.register(createQuickCommandsContributor())
  syncEngine.register(createI18nContributor())
  syncEngine.register(createNotificationSettingsContributor())

  // Populate the default module list so setActiveProvider() can auto-enable all.
  setAllContributorIds(syncEngine.getContributors().map((c) => c.id))
}

// Test-only: override the engine instance so use-sync-store tests can
// inject a minimal contributor set without pulling in real stores.
let _engineForTests: typeof syncEngine | null = null

/** @internal tests only */
export function __setEngineForTests(e: typeof syncEngine | null): void {
  _engineForTests = e
}

/** @internal tests only */
export function __getActiveEngine(): typeof syncEngine {
  return _engineForTests ?? syncEngine
}
