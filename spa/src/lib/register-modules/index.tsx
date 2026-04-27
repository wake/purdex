/* eslint-disable react-refresh/only-export-components */
import { useState } from 'react'
import { FolderOpen } from '@phosphor-icons/react'
import { getModules, registerModule } from '../module-registry'
import { registerNewTabProvider } from '../new-tab-registry'
import { registerSettingsSection } from '../settings-section-registry'
import {
  dispatchSettingsContributions,
  resetSettingsContributionsForHmr,
} from '../dispatch-settings-contributions'
import { findPane } from '../pane-tree'
import { getPlatformCapabilities } from '../platform'
import { SessionPaneContent } from '../../components/SessionPaneContent'
import { NewTabPage } from '../../components/NewTabPage'
import { DashboardPage } from '../../components/DashboardPage'
import { HistoryPage } from '../../components/HistoryPage'
import { SettingsPage } from '../../components/SettingsPage'
import { SessionSection } from '../../components/SessionSection'
import { BrowserPane } from '../../components/BrowserPane'
import { BrowserNewTabSection } from '../../components/BrowserNewTabSection'
import { MemoryMonitorPage } from '../../components/MemoryMonitorPage'
import { HostPage } from '../../components/HostPage'
import { AppearanceSection } from '../../components/settings/AppearanceSection'
import { TerminalSection } from '../../components/settings/TerminalSection'
import { ElectronSection } from '../../components/settings/ElectronSection'
import { DevEnvironmentSection } from '../../components/settings/DevEnvironmentSection'
import { TmuxAgentMonitorSection } from '../../components/settings/TmuxAgentMonitorSection'
import { ModulesSwitchboardSection } from '../../components/settings/ModulesSwitchboardSection'
import { SyncSection } from '../../components/settings/SyncSection'
import { FileTreeWorkspaceView } from '../../components/FileTreeView'
import { FileTreeSessionView } from '../../components/FileTreeSessionView'
import { useTabStore } from '../../stores/useTabStore'
import type { PaneContent } from '../../types/tab'
import type { PaneRendererProps } from '../module-registry'
import { registerSyncContributors } from '../sync/register-sync'
import {
  registerInterfaceSubsection,
  getInterfaceSubsections,
} from '../interface-subsection-registry'
import { InterfaceSection } from '../../components/settings/InterfaceSection'
import { NewTabSubsection } from '../../components/settings/new-tab/NewTabSubsection'
import { registerBuiltinTerminalLinks } from '../terminal-link'
import { computeClusterInsertTarget } from '../tab-insert/compute-cluster-insert-target'
import { fetchSessionCwd, fetchSessionHome } from '../host-api'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { openBrowserTab } from '../open-browser-tab'
import { getDefaultOpener } from '../file-opener-registry'
import { setHostBuiltinSections } from '../host-builtin-sections'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { OverviewSection } from '../../components/hosts/OverviewSection'
import { SessionsSection } from '../../components/hosts/SessionsSection'
import { HooksSection } from '../../components/hosts/HooksSection'
import { AgentsSection } from '../../components/hosts/AgentsSection'
import { UploadSection } from '../../components/hosts/UploadSection'
import { LogsSection } from '../../components/hosts/LogsSection'
import { editorModuleDefinition, registerEditorNewTabProviders } from './editor-module'
import { registerBuiltinFsBackends } from './fs-backends'
import { applyModuleFileOpeners } from './module-file-openers'
import { clearAllForHmr as clearFileOpenerRegistryForHmr } from '../file-opener-registry'

function NewTabPaneWrapper({ pane }: PaneRendererProps) {
  const handleSelect = (content: PaneContent) => {
    const { tabs } = useTabStore.getState()
    const tabId = Object.keys(tabs).find((id) =>
      findPane(tabs[id].layout, pane.id) !== undefined,
    )
    if (!tabId) return
    useTabStore.getState().setPaneContent(tabId, pane.id, content)
    useTabStore.getState().setActiveTab(tabId)
  }
  return <NewTabPage onSelect={handleSelect} />
}

function BrowserPaneWrapper({ pane }: PaneRendererProps) {
  const content = pane.content
  if (content.kind !== 'browser') return null
  return <BrowserPane paneId={pane.id} url={content.url} />
}

function MemoryMonitorPaneWrapper() {
  return <MemoryMonitorPage />
}

function InterfaceSectionHost() {
  const subs = getInterfaceSubsections()
  const [active, setActive] = useState<string>(() => subs[0]?.id ?? '')
  if (subs.length === 0) {
    // Defensive: registry not populated yet (e.g. HMR ordering race).
    // Avoid blank-area silent failure; next render should succeed.
    return <div className="flex-1 p-6 text-sm text-text-muted">Loading...</div>
  }
  return <InterfaceSection activeSubsection={active} onSelectSubsection={setActive} />
}

export { dispatchSettingsContributions } from '../dispatch-settings-contributions'

/**
 * Clear every entry in the file-opener registry. Wired into HMR dispose so
 * editing this module (or any module that owns openers) doesn't leave stale
 * duplicates after re-import. Re-registration runs through
 * `applyModuleFileOpeners()` at the next bootstrap.
 */
export function resetFileOpenerRegistryForHmr(): void {
  clearFileOpenerRegistryForHmr()
}

if (import.meta.hot) {
  import.meta.hot.dispose(() => {
    // F2 + F4: the HMR dispose hook must clear BOTH the committed
    // contribution registry AND the legacy adapter's pending buffers, so
    // no stale reserved / active entries leak across HMR re-runs.
    // `resetSettingsContributionsForHmr()` is the canonical single entry
    // point that keeps the write-side registry APIs off this module's
    // import surface (lint-enforced by F4).
    resetSettingsContributionsForHmr()
    resetFileOpenerRegistryForHmr()
  })
}

export function registerBuiltinModules(): void {
  const caps = getPlatformCapabilities()

  // Sync contributors
  registerSyncContributors()

  // Modules with pane renderers
  registerModule({
    id: 'new-tab',
    name: 'New Tab',
    panes: [{ kind: 'new-tab', component: NewTabPaneWrapper }],
  })
  registerModule({
    id: 'session',
    name: 'Session',
    panes: [{ kind: 'tmux-session', component: SessionPaneContent }],
  })
  registerModule({
    id: 'dashboard',
    name: 'Dashboard',
    panes: [{ kind: 'dashboard', component: DashboardPage }],
  })
  registerModule({
    id: 'history',
    name: 'History',
    panes: [{ kind: 'history', component: HistoryPage }],
  })
  registerModule({
    id: 'settings',
    name: 'Settings',
    panes: [{ kind: 'settings', component: SettingsPage }],
  })
  registerModule({
    id: 'browser',
    name: 'Browser',
    disableable: true,
    descriptionKey: 'modules.browser.description',
    panes: [{ kind: 'browser', component: BrowserPaneWrapper }],
  })
  registerModule({
    id: 'memory-monitor',
    name: 'Memory Monitor',
    disableable: true,
    descriptionKey: 'modules.memory_monitor.description',
    panes: [{ kind: 'memory-monitor', component: MemoryMonitorPaneWrapper }],
  })
  registerModule({
    id: 'hosts',
    name: 'Hosts',
    panes: [{ kind: 'hosts', component: HostPage }],
  })

  // Editor module
  registerModule(editorModuleDefinition)

  // Quick Commands v2 — Phase 1a registers the module shell only (disableable
  // gate + Modules Switchboard listing). Settings contribution + UI surfaces
  // ship together in Phase 1b (spec §6 — never ship a "configured but no
  // effect" intermediate state).
  registerModule({
    id: 'quick-commands',
    name: 'Quick Commands',
    disableable: true,
    descriptionKey: 'modules.quick_commands.description',
  })

  // FS backends
  registerBuiltinFsBackends(caps)

  registerModule({
    id: 'files',
    name: 'Files',
    // SR-2 (codex review #617): intentionally NOT flagged disableable yet.
    // The module's only settings surface lives in `workspaceConfig`, which
    // `WorkspaceSettingsPage` renders through `ModuleConfigSection` — a path
    // that does not consult `useModuleEnabledStore`. Toggling would be a lie
    // until PR 3 wires workspace-scope legacy contributions into the filter.
    workspaceConfig: [
      { key: 'projectPath', type: 'string', label: '專案路徑' },
    ],
    views: [
      {
        id: 'file-tree-workspace',
        label: 'Files (Workspace)',
        icon: FolderOpen,
        scope: 'workspace',
        component: FileTreeWorkspaceView,
      },
      {
        id: 'file-tree-session',
        label: 'Files (Session)',
        icon: FolderOpen,
        scope: 'tab',
        component: FileTreeSessionView,
      },
    ],
  })

  // Settings sections
  registerSettingsSection({ id: 'appearance', label: 'settings.section.appearance', order: 0, component: AppearanceSection })
  registerSettingsSection({ id: 'terminal', label: 'settings.section.terminal', order: 1, component: TerminalSection })
  registerSettingsSection({
    id: 'interface',
    label: 'settings.section.interface',
    order: 2,
    component: InterfaceSectionHost,
  })
  registerSettingsSection({ id: 'sync', label: 'settings.section.sync', order: 11, component: SyncSection })
  // Modules Switchboard — replaces the long-dormant `globalConfig` UI with a
  // module enable/disable panel. Keeps the id `module-config` for URL
  // stability (`/settings/module-config`).
  registerSettingsSection({
    id: 'module-config',
    label: 'settings.section.modules',
    order: 8,
    component: ModulesSwitchboardSection,
  })

  // Interface subsections
  registerInterfaceSubsection({
    id: 'new-tab',
    label: 'settings.interface.new_tab',
    order: 0,
    component: NewTabSubsection,
  })
  registerInterfaceSubsection({
    id: 'pane',
    label: 'settings.interface.pane',
    order: 1,
    component: () => null,
    disabled: true,
    disabledReason: 'settings.coming_soon',
  })
  registerInterfaceSubsection({
    id: 'sidebar',
    label: 'settings.interface.sidebar',
    order: 2,
    component: () => null,
    disabled: true,
    disabledReason: 'settings.coming_soon',
  })

  // New-tab providers
  registerNewTabProvider({
    id: 'sessions',
    label: 'session.provider_label',
    icon: 'List',
    order: 0,
    component: SessionSection,
  })

  registerEditorNewTabProviders()

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

  if (import.meta.env.DEV || caps.devUpdateEnabled) {
    registerSettingsSection({
      id: 'tmux-agent-monitor',
      label: 'settings.section.tmux_agent_monitor',
      order: 21,
      component: TmuxAgentMonitorSection,
    })
  }

  registerBuiltinTerminalLinks({
    urlOpener: {
      isElectron: caps.isElectron,
      openBrowserTab,
      openMiniWindow: (url) => window.electronAPI?.browserViewOpenMiniWindow(url),
    },
    filePathOpener: {
      getDefaultOpener,
      openSingletonTab: (content, opts) => useTabStore.getState().openSingletonTab(content, opts),
      insertTab: (tabId, wsId, afterTabId) => useWorkspaceStore.getState().insertTab(tabId, wsId, afterTabId),
      getActiveWorkspaceId: () => useWorkspaceStore.getState().activeWorkspaceId,
      computeInsertTarget: (wsId, isSameKind) => computeClusterInsertTarget(wsId, isSameKind),
      fetchPaneCwd: (hostId, sessionCode, signal) => fetchSessionCwd(hostId, sessionCode, signal),
      fetchPaneHome: (hostId, sessionCode, signal) => fetchSessionHome(hostId, sessionCode, signal),
    },
  })

  // Built-in host sub-page contributions (PR-4 + #586).
  // Atomically replace the full set of built-in host sources; any localId
  // not in this list would be dropped.  Re-materialized by every
  // dispatchSettingsContributions() call (idempotent).  Wrapper identity is
  // stable per localId across HMR reloads.
  setHostBuiltinSections([
    { localId: 'overview',  labelKey: 'hosts.overview',  order: 0, component: OverviewSection },
    { localId: 'sessions',  labelKey: 'hosts.sessions',  order: 1, component: SessionsSection },
    { localId: 'hooks',     labelKey: 'hosts.hooks',     order: 2, component: HooksSection },
    { localId: 'agents',    labelKey: 'hosts.agents',    order: 3, component: AgentsSection },
    { localId: 'uploads',   labelKey: 'hosts.uploads',   order: 4, component: UploadSection },
    { localId: 'logs',      labelKey: 'hosts.logs',      order: 5, component: LogsSection },
  ])

  // Reconcile module-declared file openers with the file-opener registry.
  // Must run after every registerModule(...) call so getModules() returns the
  // fully populated set; iterating earlier would miss late-registered modules.
  // No-op until Task 1.3 promotes Editor's inline file openers into
  // editorModuleDefinition.fileOpeners — at which point this becomes the
  // authoritative wire-up and registerEditorFileOpeners() goes away.
  applyModuleFileOpeners()

  // Capture the module-enabled baseline for the Modules Switchboard. Runs
  // after all registerModule(...) calls so `getModules()` returns the fully
  // populated set with their `disableable` flags resolved. `captureBaseline`
  // is a first-call-wins no-op, so HMR re-runs of this function don't
  // overwrite the session baseline (spec I7).
  const baselineSnapshot: Record<string, boolean> = {}
  for (const m of getModules()) {
    if (m.disableable === true) {
      baselineSnapshot[m.id] = useModuleEnabledStore.getState().isEnabled(m.id)
    }
  }
  useModuleEnabledStore.getState().captureBaseline(baselineSnapshot)

  // Dispatch module-declared settings contributions into the contribution registry.
  // Must run AFTER all registerModule(...) calls so every module is visible.
  dispatchSettingsContributions()
}
