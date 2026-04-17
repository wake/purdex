import { contextBridge, ipcRenderer } from 'electron'

contextBridge.exposeInMainWorld('electronAPI', {
  // Window Management
  tearOffTab: (tabJson: string) => ipcRenderer.invoke('window:tear-off', tabJson),
  mergeTab: (tabJson: string, targetWindowId: string) =>
    ipcRenderer.invoke('window:merge', tabJson, targetWindowId),
  getWindows: () => ipcRenderer.invoke('window:get-all'),
  onTabReceived: (callback: (tabJson: string, replace: boolean) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, tabJson: string, replace?: boolean) =>
      callback(tabJson, replace ?? false)
    ipcRenderer.on('tab:received', handler)
    return () => ipcRenderer.removeListener('tab:received', handler)
  },

  // Workspace Management
  tearOffWorkspace: (payload: string) => ipcRenderer.invoke('window:tear-off-workspace', payload),
  mergeWorkspace: (payload: string, targetWindowId: string) =>
    ipcRenderer.invoke('window:merge-workspace', payload, targetWindowId),
  onWorkspaceReceived: (callback: (payload: string, replace: boolean) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, payload: string, replace?: boolean) =>
      callback(payload, replace ?? false)
    ipcRenderer.on('workspace:received', handler)
    return () => ipcRenderer.removeListener('workspace:received', handler)
  },

  // Browser View — existing
  openBrowserView: (url: string, paneId: string) =>
    ipcRenderer.invoke('browser-view:open', url, paneId),
  closeBrowserView: (paneId: string) =>
    ipcRenderer.invoke('browser-view:close', paneId),
  navigateBrowserView: (paneId: string, url: string) =>
    ipcRenderer.invoke('browser-view:navigate', paneId, url),
  resizeBrowserView: (paneId: string, bounds: { x: number; y: number; width: number; height: number }) =>
    ipcRenderer.invoke('browser-view:resize', paneId, JSON.stringify(bounds)),

  // Browser View — navigation
  browserViewGoBack: (paneId: string) =>
    ipcRenderer.invoke('browser-view:go-back', paneId),
  browserViewGoForward: (paneId: string) =>
    ipcRenderer.invoke('browser-view:go-forward', paneId),
  browserViewReload: (paneId: string) =>
    ipcRenderer.invoke('browser-view:reload', paneId),
  browserViewStop: (paneId: string) =>
    ipcRenderer.invoke('browser-view:stop', paneId),
  browserViewPrint: (paneId: string) =>
    ipcRenderer.invoke('browser-view:print', paneId),

  // Browser View — lifecycle
  destroyBrowserView: (paneId: string) =>
    ipcRenderer.invoke('browser-view:destroy', paneId),

  // Browser View — mini window
  browserViewOpenMiniWindow: (url: string) =>
    ipcRenderer.invoke('browser-view:open-mini-window', url),
  browserViewMoveToTab: (paneId: string) =>
    ipcRenderer.invoke('browser-view:move-to-tab', paneId),

  // Browser View — request state (SPA catches up after late mount)
  requestBrowserViewState: (paneId: string) =>
    ipcRenderer.invoke('browser-view:request-state', paneId),

  // Browser View — state subscription (Electron → SPA)
  onBrowserViewStateUpdate: (callback: (paneId: string, state: unknown) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, paneId: string, state: unknown) =>
      callback(paneId, state)
    ipcRenderer.on('browser-view:state-update', handler)
    return () => ipcRenderer.removeListener('browser-view:state-update', handler)
  },

  // Browser View — open in tab (from mini browser or WebContentsView link click)
  onBrowserViewOpenInTab: (callback: (url: string) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, url: string) => callback(url)
    ipcRenderer.on('browser-view:open-in-tab', handler)
    return () => ipcRenderer.removeListener('browser-view:open-in-tab', handler)
  },

  // SPA ready signal
  signalReady: () => ipcRenderer.send('spa:ready'),

  // SPA reload (re-detect dev server via main process)
  reloadSPA: () => ipcRenderer.invoke('spa:reload'),

  // Keyboard Shortcuts
  onShortcut: (callback: (payload: { action: string }) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, payload: { action: string }) =>
      callback(payload)
    ipcRenderer.on('shortcut:execute', handler)
    return () => ipcRenderer.removeListener('shortcut:execute', handler)
  },

  // Memory Monitor
  getProcessMetrics: () => ipcRenderer.invoke('metrics:get'),
  onMetricsUpdate: (callback: (metrics: unknown[]) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, metrics: unknown[]) => callback(metrics)
    ipcRenderer.on('metrics:update', handler)
    return () => ipcRenderer.removeListener('metrics:update', handler)
  },

  // Notifications
  showNotification: (opts: { title: string; body: string; sessionCode: string; eventName: string; broadcastTs: number; action?: { kind: string; hostId: string; sessionCode?: string } }) =>
    ipcRenderer.invoke('notification:show', JSON.stringify(opts)),
  onNotificationClicked: (callback: (payload: { sessionCode: string; action?: { kind: string; hostId: string; sessionCode?: string } }) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, payload: { sessionCode: string; action?: { kind: string; hostId: string; sessionCode?: string } }) =>
      callback(payload)
    ipcRenderer.on('notification:clicked', handler)
    return () => ipcRenderer.removeListener('notification:clicked', handler)
  },
  focusMyWindow: () => ipcRenderer.send('notification:focus-window'),

  // Filesystem (LocalBackend)
  fs: {
    read: (path: string) => ipcRenderer.invoke('fs:read', path),
    write: (path: string, content: Uint8Array) => ipcRenderer.invoke('fs:write', path, content),
    stat: (path: string) => ipcRenderer.invoke('fs:stat', path),
    list: (path: string) => ipcRenderer.invoke('fs:list', path),
    mkdir: (path: string, recursive: boolean) => ipcRenderer.invoke('fs:mkdir', path, recursive),
    delete: (path: string, recursive: boolean) => ipcRenderer.invoke('fs:delete', path, recursive),
    rename: (from: string, to: string) => ipcRenderer.invoke('fs:rename', from, to),
  },

  // Dev Update (only exposed when PDX_DEV_UPDATE=1)
  ...(process.env.PDX_DEV_UPDATE ? {
    getAppInfo: () => ipcRenderer.invoke('dev:app-info'),
    checkUpdate: (daemonUrl: string, token?: string) => ipcRenderer.invoke('dev:check-update', daemonUrl, token),
    applyUpdate: (daemonUrl: string, token?: string) => ipcRenderer.invoke('dev:apply-update', daemonUrl, token),
    forceLoadSPA: (mode: 'dev' | 'bundled') => ipcRenderer.invoke('spa:force-load', mode),
    onUpdateProgress: (callback: (step: string) => void) => {
      const handler = (_event: Electron.IpcRendererEvent, step: string) => callback(step)
      ipcRenderer.on('dev:update-progress', handler)
      return () => ipcRenderer.removeListener('dev:update-progress', handler)
    },
    streamCheck: (
      daemonUrl: string,
      token: string | undefined,
      onEvent: (ev: unknown) => void,
    ) => {
      const handler = (_event: Electron.IpcRendererEvent, ev: unknown) => onEvent(ev)
      ipcRenderer.on('dev:stream-check-event', handler)
      ipcRenderer.invoke('dev:stream-check', daemonUrl, token).catch(() => {
        // Errors arrive as dev:stream-check-event { type:'error' } — ignore the
        // rejection here.
      })
      return () => {
        ipcRenderer.send('dev:stream-check-stop')
        ipcRenderer.removeListener('dev:stream-check-event', handler)
      }
    },
  } : {}),
})
