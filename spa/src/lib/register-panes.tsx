import { registerPaneRenderer } from './pane-registry'
import { registerNewTabProvider } from './new-tab-registry'
import { registerSettingsSection } from './settings-section-registry'
import { findPane } from './pane-tree'
import { SessionPaneContent } from '../components/SessionPaneContent'
import { NewTabPage } from '../components/NewTabPage'
import { DashboardPage } from '../components/DashboardPage'
import { HistoryPage } from '../components/HistoryPage'
import { SettingsPage } from '../components/SettingsPage'
import { SessionSection } from '../components/SessionSection'
import { AppearanceSection } from '../components/settings/AppearanceSection'
import { TerminalSection } from '../components/settings/TerminalSection'
import { useTabStore } from '../stores/useTabStore'
import type { PaneContent } from '../types/tab'

export function registerBuiltinPanes(): void {
  // Pane renderers
  registerPaneRenderer('new-tab', {
    component: ({ pane }) => {
      const handleSelect = (content: PaneContent) => {
        const { tabs } = useTabStore.getState()
        // Find which tab contains this pane (not necessarily activeTabId)
        const tabId = Object.keys(tabs).find((id) =>
          findPane(tabs[id].layout, pane.id) !== undefined,
        )
        if (!tabId) return
        useTabStore.getState().setPaneContent(tabId, pane.id, content)
        useTabStore.getState().setActiveTab(tabId)
      }
      return <NewTabPage onSelect={handleSelect} />
    },
  })
  registerPaneRenderer('session', { component: SessionPaneContent })
  registerPaneRenderer('dashboard', { component: DashboardPage })
  registerPaneRenderer('history', { component: HistoryPage })
  registerPaneRenderer('settings', { component: SettingsPage })

  // Settings sections
  registerSettingsSection({ id: 'appearance', label: 'Appearance', order: 0, component: AppearanceSection })
  registerSettingsSection({ id: 'terminal', label: 'Terminal', order: 1, component: TerminalSection })
  registerSettingsSection({ id: 'workspace', label: 'Workspace', order: 10 }) // reserved
  registerSettingsSection({ id: 'sync', label: 'Sync', order: 11 }) // reserved

  // New-tab providers
  registerNewTabProvider({
    id: 'sessions',
    label: 'Sessions',
    icon: 'List',
    order: 0,
    component: SessionSection,
  })
}
