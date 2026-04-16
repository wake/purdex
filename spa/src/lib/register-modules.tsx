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
import { TerminalSection } from '../components/settings/TerminalSection'
import { ElectronSection } from '../components/settings/ElectronSection'
import { DevEnvironmentSection } from '../components/settings/DevEnvironmentSection'
import { ModuleConfigSection } from '../components/settings/ModuleConfigSection'
import { FileTreeWorkspaceView } from '../components/FileTreeView'
import { FileTreeSessionView } from '../components/FileTreeSessionView'
import { FolderOpen } from '@phosphor-icons/react'
import { useTabStore } from '../stores/useTabStore'
import type { PaneContent } from '../types/tab'
import type { PaneRendererProps } from './module-registry'
import { EditorPane } from '../components/editor/EditorPane'
import { ImagePreviewPane } from '../components/editor/ImagePreviewPane'
import { PdfPreviewPane } from '../components/editor/PdfPreviewPane'
import { EditorNewTabSection } from '../components/editor/EditorNewTabSection'
import { InAppBackend } from './fs-backend-inapp'
import { DaemonBackend } from './fs-backend-daemon'
import { LocalBackend } from './fs-backend-local'
import { registerFsBackend, getFsBackend } from './fs-backend'
import { registerFileOpener } from './file-opener-registry'
import { useHostStore } from '../stores/useHostStore'

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

export function registerBuiltinModules(): void {
  const caps = getPlatformCapabilities()

  // Modules with pane renderers
  registerModule({
    id: 'new-tab',
    name: 'New Tab',
    panes: [{
      kind: 'new-tab',
      component: NewTabPaneWrapper,
    }],
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
    panes: [{
      kind: 'browser',
      component: BrowserPaneWrapper,
    }],
  })
  registerModule({
    id: 'memory-monitor',
    name: 'Memory Monitor',
    panes: [{
      kind: 'memory-monitor',
      component: MemoryMonitorPaneWrapper,
    }],
  })
  registerModule({
    id: 'hosts',
    name: 'Hosts',
    panes: [{ kind: 'hosts', component: HostPage }],
  })

  // Editor module
  registerModule({
    id: 'editor',
    name: 'Editor',
    panes: [
      { kind: 'editor', component: EditorPane },
      { kind: 'image-preview', component: ImagePreviewPane },
      { kind: 'pdf-preview', component: PdfPreviewPane },
    ],
  })

  // Register InApp FS backend (singleton — 避免熱重載時資料遺失)
  if (!getFsBackend({ type: 'inapp' })) {
    registerFsBackend('inapp', new InAppBackend())
  }

  // Register DaemonBackend (lazy proxy — creates a new DaemonBackend per call,
  // resolving active host at invocation time. This is intentional: the active host
  // can change at any time and DaemonBackend is stateless. If DaemonBackend gains
  // internal state, switch to a memoized-by-hostId pattern.)
  if (!getFsBackend({ type: 'daemon', hostId: '' })) {
    const getDaemon = (): DaemonBackend => {
      const state = useHostStore.getState()
      const hostId = state.activeHostId ?? state.hostOrder[0] ?? ''
      return new DaemonBackend(
        state.getDaemonBase(hostId),
        () => state.getAuthHeaders(hostId),
      )
    }

    registerFsBackend('daemon', {
      id: 'daemon',
      label: 'Remote Host',
      available: () => !!useHostStore.getState().activeHostId,
      read: (path) => getDaemon().read(path),
      write: (path, content) => getDaemon().write(path, content),
      stat: (path) => getDaemon().stat(path),
      list: (path) => getDaemon().list(path),
      mkdir: (path, recursive) => getDaemon().mkdir(path, recursive),
      delete: (path, recursive) => getDaemon().delete(path, recursive),
      rename: (from, to) => getDaemon().rename(from, to),
    })
  }

  // Register LocalBackend (Electron IPC — local filesystem access)
  if (caps.hasLocalFilesystem && !getFsBackend({ type: 'local' })) {
    registerFsBackend('local', new LocalBackend())
  }

  // Register file openers for binary previews
  const IMAGE_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'ico'])
  const PDF_EXTS = new Set(['pdf'])

  registerFileOpener({
    id: 'image-preview',
    label: 'Image Preview',
    icon: 'Image',
    match: (file) => IMAGE_EXTS.has(file.extension.toLowerCase()),
    priority: 'default',
    createContent: (source, file) => ({ kind: 'image-preview', source, filePath: file.path }) as PaneContent,
  })

  registerFileOpener({
    id: 'pdf-viewer',
    label: 'PDF Viewer',
    icon: 'FilePdf',
    match: (file) => PDF_EXTS.has(file.extension.toLowerCase()),
    priority: 'default',
    createContent: (source, file) => ({ kind: 'pdf-preview', source, filePath: file.path }) as PaneContent,
  })

  // Register file opener for text files (excludes image/PDF extensions)
  const BINARY_EXTS = new Set([...IMAGE_EXTS, ...PDF_EXTS])
  registerFileOpener({
    id: 'monaco-editor',
    label: 'Text Editor',
    icon: 'File',
    match: (file) => !file.isDirectory && !BINARY_EXTS.has(file.extension.toLowerCase()),
    priority: 'default',
    createContent: (source, file) => ({ kind: 'editor', source, filePath: file.path }) as PaneContent,
  })

  registerModule({
    id: 'files',
    name: 'Files',
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
  registerSettingsSection({ id: 'workspace', label: 'settings.section.workspace', order: 10 }) // reserved
  registerSettingsSection({ id: 'sync', label: 'settings.section.sync', order: 11 }) // reserved
  registerSettingsSection({
    id: 'module-config',
    label: 'settings.section.modules',
    order: 8,
    component: () => <ModuleConfigSection scope="global" />,
  })

  // New-tab providers
  registerNewTabProvider({
    id: 'sessions',
    label: 'session.provider_label',
    icon: 'List',
    order: 0,
    component: SessionSection,
  })

  registerNewTabProvider({
    id: 'editor',
    label: 'editor.provider_label',
    icon: 'File',
    order: 5,
    component: EditorNewTabSection,
  })

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
