import { registerPaneRenderer } from './pane-registry'
import { registerNewTabProvider } from './new-tab-registry'
import { SessionPaneContent } from '../components/SessionPaneContent'
import { NewTabPage } from '../components/NewTabPage'
import { DashboardPage } from '../components/DashboardPage'
import { HistoryPage } from '../components/HistoryPage'
import { SettingsPage } from '../components/SettingsPage'
import { SessionSection } from '../components/SessionSection'
import { useTabStore } from '../stores/useTabStore'
import type { PaneContent } from '../types/tab'

export function registerBuiltinPanes(): void {
  // Pane renderers
  registerPaneRenderer('new-tab', {
    component: ({ pane }) => {
      const handleSelect = (content: PaneContent) => {
        const { activeTabId } = useTabStore.getState()
        if (!activeTabId) return
        useTabStore.getState().setPaneContent(activeTabId, pane.id, content)
      }
      return <NewTabPage onSelect={handleSelect} />
    },
  })
  registerPaneRenderer('session', { component: SessionPaneContent })
  registerPaneRenderer('dashboard', { component: () => <DashboardPage /> })
  registerPaneRenderer('history', { component: HistoryPage })
  registerPaneRenderer('settings', { component: () => <SettingsPage /> })

  // New-tab providers
  registerNewTabProvider({
    id: 'sessions',
    label: 'Sessions',
    icon: 'List',
    order: 0,
    component: SessionSection,
  })
}
