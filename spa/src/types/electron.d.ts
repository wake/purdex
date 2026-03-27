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

interface Window {
  electronAPI?: {
    tearOffTab: (tabJson: string) => Promise<void>
    mergeTab: (tabJson: string, targetWindowId: string) => Promise<void>
    openBrowserView: (url: string, paneId: string) => Promise<void>
    closeBrowserView: (paneId: string) => Promise<void>
    navigateBrowserView: (paneId: string, url: string) => Promise<void>
    onTabReceived: (callback: (tabJson: string) => void) => () => void
    signalReady: () => void

    // Window Management
    getWindows: () => Promise<ElectronWindowInfo[]>

    // Browser View
    resizeBrowserView: (paneId: string, bounds: ElectronBounds) => Promise<void>

    // Memory Monitor
    getProcessMetrics: () => Promise<ElectronTabMetrics[]>
    onMetricsUpdate: (callback: (metrics: ElectronTabMetrics[]) => void) => () => void

    // Dev Update
    getAppInfo: () => Promise<ElectronAppInfo>
    checkUpdate: (daemonUrl: string) => Promise<{ version: string; spaHash: string; electronHash: string }>
    applyUpdate: (daemonUrl: string) => Promise<ElectronUpdateResult>
  }
}
