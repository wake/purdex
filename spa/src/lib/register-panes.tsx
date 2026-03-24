import { registerPaneRenderer } from './pane-registry'
import { registerNewTabProvider } from './new-tab-registry'
import { SessionPaneContent } from '../components/SessionPaneContent'
import { NewTabPage } from '../components/NewTabPage'
import { DashboardPage } from '../components/DashboardPage'
import { HistoryPage } from '../components/HistoryPage'
import { SettingsPage } from '../components/SettingsPage'
import { SessionSection } from '../components/SessionSection'
import { useTabStore } from '../stores/useTabStore'
import { updatePaneInLayout } from './pane-tree'
import type { PaneContent } from '../types/tab'

export function registerBuiltinPanes(): void {
  // Pane renderers
  registerPaneRenderer('new-tab', {
    component: ({ pane }) => {
      const handleSelect = (content: PaneContent) => {
        // Replace this pane's content with the selected content
        const { tabs, activeTabId } = useTabStore.getState()
        if (!activeTabId) return
        const tab = tabs[activeTabId]
        if (!tab) return
        const newLayout = updatePaneInLayout(tab.layout, pane.id, content)
        useTabStore.setState({
          tabs: { ...tabs, [activeTabId]: { ...tab, layout: newLayout } },
        })
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
