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
}

interface Window {
  electronAPI?: {
    tearOffTab: (tabJson: string) => Promise<void>
    mergeTab: (tabJson: string, targetWindowId: string) => Promise<void>
    openBrowserView: (url: string, paneId: string) => Promise<void>
    closeBrowserView: (paneId: string) => Promise<void>
    navigateBrowserView: (paneId: string, url: string) => Promise<void>
    onTabReceived: (callback: (tabJson: string, replace: boolean) => void) => () => void
    signalReady: () => void
    reloadSPA: () => Promise<void>
    forceLoadSPA: (mode: 'dev' | 'bundled') => Promise<void>

    // Keyboard Shortcuts
    onShortcut: (callback: (payload: { action: string }) => void) => () => void

    // Window Management
    getWindows: () => Promise<ElectronWindowInfo[]>

    // Browser View
    resizeBrowserView: (paneId: string, bounds: ElectronBounds) => Promise<void>

    // Memory Monitor
    getProcessMetrics: () => Promise<ElectronTabMetrics[]>
    onMetricsUpdate: (callback: (metrics: ElectronTabMetrics[]) => void) => () => void

    // Notifications
    showNotification: (opts: { title: string; body: string; sessionCode: string; eventName: string; broadcastTs: number }) => Promise<void>
    onNotificationClicked: (callback: (payload: { sessionCode: string }) => void) => () => void
    focusMyWindow: () => void

    // Dev Update
    getAppInfo: () => Promise<ElectronAppInfo>
    checkUpdate: (daemonUrl: string) => Promise<ElectronRemoteVersionInfo>
    applyUpdate: (daemonUrl: string) => Promise<ElectronUpdateResult>
    onUpdateProgress: (callback: (step: string) => void) => () => void
  }
}
