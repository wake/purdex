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

interface ElectronLocalDaemonStatus {
  managed: 'none' | 'managed' | 'external'
  reason?: string
  binPath: string
  installed: { version: string; hash: string; goos: string; goarch: string } | null
  alive: { pid: number } | null
  running: { version: string; hash: string; url: string } | null
  config: { bind: string; port: number; token: string | null } | null
  hostname: string
  target: { goos: 'darwin' | 'linux'; goarch: 'arm64' | 'amd64' }
  tools: { tmux: string | null }
  /** How `pdx` is reachable on the daemon's launch PATH; absent when there is
   *  no installed binary able to answer (spec 2026-09-16 §4.6). */
  cli?: {
    resolved: string | null
    isManagedBinary: boolean
    pathSource: 'shell' | 'fallback'
    localBinOnPath: boolean
    link: 'ok' | 'missing' | 'conflict' | 'error'
  }
}

interface ElectronLocalDaemonPathResult {
  code: number | null
  stdout: string
  stderr: string
}

interface ElectronLocalDaemonResult {
  url: string
  token: string
  hash: string
  version: string
  hostname: string
  bindNote?: string
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
    openExternalUrl: (url: string) => Promise<void>
    browserViewMoveToTab: (paneId: string) => Promise<void>
    requestBrowserViewState: (paneId: string) => Promise<void>
    onBrowserViewStateUpdate: (callback: (paneId: string, state: { url: string; title: string; canGoBack: boolean; canGoForward: boolean; isLoading: boolean }) => void) => () => void
    onBrowserViewOpenInTab: (callback: (url: string) => void) => () => void

    // Memory Monitor
    getProcessMetrics: () => Promise<ElectronTabMetrics[]>
    onMetricsUpdate: (callback: (metrics: ElectronTabMetrics[]) => void) => () => void

    // Notifications
    // `requestId` names an approval request (open-approval); Electron main forwards `action` as the renderer sent it.
    showNotification: (opts: { title: string; body: string; sessionCode: string; eventName: string; broadcastTs: number; action?: { kind: string; hostId: string; sessionCode?: string; requestId?: string } }) => Promise<void>
    onNotificationClicked: (callback: (payload: { sessionCode: string; action?: { kind: string; hostId: string; sessionCode?: string; requestId?: string } }) => void) => () => void
    focusMyWindow: () => void

    // Dock badge (app-badge spec §3.2): this window's resolved session keys and every open card; main takes the union
    // over windows. Optional: an app build older than the SPA has no bridge, and the SPA then sends nothing.
    badge?: { set: (report: { sessions: string[]; cards: { id: string; session?: string; fallback: string }[] }) => void }

    // Deeplink (purdex://execution/<id>[?host=<hint>]) — consumed by the P.12 resolver
    onDeeplinkNavigate: (callback: (payload: { executionId: string; host?: string }) => void) => () => void

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

    // Menu bar tray (main-process preference, persisted in app-prefs.json)
    tray: {
      getVisible: () => Promise<boolean>
      setVisible: (visible: boolean) => Promise<boolean>
      /** Fired for every window whenever the main process applies a change. Returns unsubscribe. */
      onVisibilityChanged?: (callback: (visible: boolean) => void) => () => void
    }

    // Push presence (main-process reporter). Every method optional: an older shell lacks the group
    // or some methods, so callers feature-detect with optional chaining.
    presence?: {
      /** Hands the persisted host list to the main process; false when the payload is invalid. */
      setHosts?: (payload: { rev: number; hosts: Array<{ hostId: string; baseUrl: string; token: string | null }> }) => Promise<boolean>
      getAwayMinutes?: () => Promise<number>
      /** Rejects for a value outside 1/2/3/5/10/15/30/60; resolves to the saved value. */
      setAwayMinutes?: (minutes: number) => Promise<number>
      /** Host ids the main process currently reports for (v2 send succeeded within the last 45 s). */
      getOwned?: () => Promise<string[]>
      onOwnedChanged?: (callback: (hostIds: string[]) => void) => () => void
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

    // Local daemon (Electron only; absent in the web build)
    localDaemonStatus?: () => Promise<ElectronLocalDaemonStatus>
    localDaemonInstall?: (daemonUrl: string, token?: string) => Promise<ElectronLocalDaemonResult>
    localDaemonStart?: () => Promise<ElectronLocalDaemonResult>
    localDaemonRestart?: () => Promise<ElectronLocalDaemonResult>
    localDaemonPathLink?: (force?: boolean) => Promise<ElectronLocalDaemonPathResult>
    localDaemonPathAddToShell?: () => Promise<ElectronLocalDaemonPathResult>
    onLocalDaemonProgress?: (callback: (step: string) => void) => () => void
  }
}
