// spa/src/types/electron.d.ts

interface ElectronWindowInfo {
  id: string
  title: string
}

interface ElectronBounds {
  x: number
  y: number
  width: number
  height: number
}

interface ElectronTabMetrics {
  paneId: string
  kind: string
  memoryKB: number
  cpuPercent: number
  state: 'active' | 'background' | 'discarded'
}

interface ElectronAppInfo {
  version: string
  electronHash: string
  spaHash: string
  devUpdateEnabled: boolean
}

interface ElectronUpdateResult {
  success: boolean
  message: string
}

interface ElectronRemoteVersionInfo {
  version: string
  spaHash: string
  electronHash: string
  source: { spaHash: string; electronHash: string }
  building: boolean
  buildError: string
  requiresFullRebuild: boolean
  fullRebuildReason?: string
}

interface ElectronStreamCheckEvent {
  type: 'check' | 'phase' | 'stdout' | 'stderr' | 'done' | 'error'
  phase?: string
  line?: string
  error?: string
  check?: ElectronRemoteVersionInfo
}

interface Window {
  electronAPI?: {
    tearOffTab: (tabJson: string) => Promise<void>
    mergeTab: (tabJson: string, targetWindowId: string) => Promise<void>
    onTabReceived: (callback: (tabJson: string, replace: boolean) => void) => () => void

    // Workspace Management
    tearOffWorkspace: (payload: string) => Promise<void>
    mergeWorkspace: (payload: string, targetWindowId: string) => Promise<void>
    onWorkspaceReceived: (callback: (payload: string, replace: boolean) => void) => () => void

    openBrowserView: (url: string, paneId: string) => Promise<void>
    closeBrowserView: (paneId: string) => Promise<void>
    navigateBrowserView: (paneId: string, url: string) => Promise<void>
    signalReady: () => void
    reloadSPA: () => Promise<void>
    forceLoadSPA: (mode: 'dev' | 'bundled') => Promise<void>

    // Keyboard Shortcuts
    onShortcut: (callback: (payload: { action: string }) => void) => () => void

    // Window Management
    getWindows: () => Promise<ElectronWindowInfo[]>

    // Browser View
    resizeBrowserView: (paneId: string, bounds: ElectronBounds) => Promise<void>
    browserViewGoBack: (paneId: string) => Promise<void>
    browserViewGoForward: (paneId: string) => Promise<void>
    browserViewReload: (paneId: string) => Promise<void>
    browserViewStop: (paneId: string) => Promise<void>
    browserViewPrint: (paneId: string) => Promise<void>
    destroyBrowserView: (paneId: string) => Promise<void>
    browserViewOpenMiniWindow: (url: string) => Promise<void>
    browserViewMoveToTab: (paneId: string) => Promise<void>
    requestBrowserViewState: (paneId: string) => Promise<void>
    onBrowserViewStateUpdate: (callback: (paneId: string, state: { url: string; title: string; canGoBack: boolean; canGoForward: boolean; isLoading: boolean }) => void) => () => void
    onBrowserViewOpenInTab: (callback: (url: string) => void) => () => void

    // Memory Monitor
    getProcessMetrics: () => Promise<ElectronTabMetrics[]>
    onMetricsUpdate: (callback: (metrics: ElectronTabMetrics[]) => void) => () => void

    // Notifications
    showNotification: (opts: { title: string; body: string; sessionCode: string; eventName: string; broadcastTs: number; action?: { kind: string; hostId: string; sessionCode?: string } }) => Promise<void>
    onNotificationClicked: (callback: (payload: { sessionCode: string; action?: { kind: string; hostId: string; sessionCode?: string } }) => void) => () => void
    focusMyWindow: () => void

    // Filesystem (LocalBackend)
    fs: {
      read: (path: string) => Promise<Uint8Array>
      write: (path: string, content: Uint8Array) => Promise<void>
      stat: (path: string) => Promise<{ size: number; mtime: number; isDirectory: boolean; isFile: boolean }>
      list: (path: string) => Promise<Array<{ name: string; isDir: boolean; size: number }>>
      mkdir: (path: string, recursive: boolean) => Promise<void>
      delete: (path: string, recursive: boolean) => Promise<void>
      rename: (from: string, to: string) => Promise<void>
    }

    // Dev Update
    getAppInfo: () => Promise<ElectronAppInfo>
    checkUpdate: (daemonUrl: string, token?: string) => Promise<ElectronRemoteVersionInfo>
    applyUpdate: (daemonUrl: string, token?: string) => Promise<ElectronUpdateResult>
    onUpdateProgress: (callback: (step: string) => void) => () => void
    streamCheck: (
      daemonUrl: string,
      token: string | undefined,
      onEvent: (ev: ElectronStreamCheckEvent) => void,
    ) => () => void
  }
}
