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
