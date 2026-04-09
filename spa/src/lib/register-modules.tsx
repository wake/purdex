import { registerModule } from './module-registry'
import { registerNewTabProvider } from './new-tab-registry'
import { registerSettingsSection } from './settings-section-registry'
import { findPane } from './pane-tree'
import { getPlatformCapabilities } from './platform'
import { SessionPaneContent } from '../components/SessionPaneContent'
import { NewTabPage } from '../components/NewTabPage'
import { DashboardPage } from '../components/DashboardPage'
import { HistoryPage } from '../components/HistoryPage'
import { SettingsPage } from '../components/SettingsPage'
import { SessionSection } from '../components/SessionSection'
import { BrowserPane } from '../components/BrowserPane'
import { BrowserNewTabSection } from '../components/BrowserNewTabSection'
import { MemoryMonitorPage } from '../components/MemoryMonitorPage'
import { HostPage } from '../components/HostPage'
import { AppearanceSection } from '../components/settings/AppearanceSection'
import { AgentSection } from '../components/settings/AgentSection'
import { TerminalSection } from '../components/settings/TerminalSection'
import { ElectronSection } from '../components/settings/ElectronSection'
import { DevEnvironmentSection } from '../components/settings/DevEnvironmentSection'
import { FileTreeView } from '../components/FileTreeView'
import { FolderOpen } from '@phosphor-icons/react'
import { useTabStore } from '../stores/useTabStore'
import type { PaneContent } from '../types/tab'

export function registerBuiltinModules(): void {
  // Modules with pane renderers
  registerModule({
    id: 'new-tab',
    name: 'New Tab',
    pane: {
      kind: 'new-tab',
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
    },
  })
  registerModule({
    id: 'session',
    name: 'Session',
    pane: { kind: 'tmux-session', component: SessionPaneContent },
  })
  registerModule({
    id: 'dashboard',
    name: 'Dashboard',
    pane: { kind: 'dashboard', component: DashboardPage },
  })
  registerModule({
    id: 'history',
    name: 'History',
    pane: { kind: 'history', component: HistoryPage },
  })
  registerModule({
    id: 'settings',
    name: 'Settings',
    pane: { kind: 'settings', component: SettingsPage },
  })
  registerModule({
    id: 'browser',
    name: 'Browser',
    pane: {
      kind: 'browser',
      component: ({ pane }) => {
        const content = pane.content
        if (content.kind !== 'browser') return null
        return <BrowserPane paneId={pane.id} url={content.url} />
      },
    },
  })
  registerModule({
    id: 'memory-monitor',
    name: 'Memory Monitor',
    pane: {
      kind: 'memory-monitor',
      component: () => <MemoryMonitorPage />,
    },
  })
  registerModule({
    id: 'hosts',
    name: 'Hosts',
    pane: { kind: 'hosts', component: HostPage },
  })
  registerModule({
    id: 'files',
    name: 'Files',
    views: [{
      id: 'file-tree',
      label: 'Files',
      icon: FolderOpen,
      scope: 'workspace',
      defaultRegion: 'primary-panel',
      component: FileTreeView,
    }],
  })

  // Settings sections
  registerSettingsSection({ id: 'appearance', label: 'settings.section.appearance', order: 0, component: AppearanceSection })
  registerSettingsSection({ id: 'terminal', label: 'settings.section.terminal', order: 1, component: TerminalSection })
  registerSettingsSection({ id: 'agent', label: 'settings.section.agent', order: 2, component: AgentSection })
  registerSettingsSection({ id: 'workspace', label: 'settings.section.workspace', order: 10 }) // reserved
  registerSettingsSection({ id: 'sync', label: 'settings.section.sync', order: 11 }) // reserved

  // New-tab providers
  registerNewTabProvider({
    id: 'sessions',
    label: 'session.provider_label',
    icon: 'List',
    order: 0,
    component: SessionSection,
  })

  const caps = getPlatformCapabilities()

  registerNewTabProvider({
    id: 'browser',
    label: 'browser.provider_label',
    icon: 'Globe',
    order: -10,
    component: BrowserNewTabSection,
    disabled: !caps.canBrowserPane,
    disabledReason: 'browser.requires_app',
  })

  if (caps.canSystemTray) {
    registerSettingsSection({
      id: 'electron',
      label: 'settings.section.electron',
      order: 5,
      component: ElectronSection,
    })
  }

  if (caps.devUpdateEnabled) {
    registerSettingsSection({
      id: 'dev-environment',
      label: 'settings.section.dev_environment',
      order: 20,
      component: DevEnvironmentSection,
    })
  }
}
