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

  // Browser View
  openBrowserView: (url: string, paneId: string) =>
    ipcRenderer.invoke('browser-view:open', url, paneId),
  closeBrowserView: (paneId: string) =>
    ipcRenderer.invoke('browser-view:close', paneId),
  navigateBrowserView: (paneId: string, url: string) =>
    ipcRenderer.invoke('browser-view:navigate', paneId, url),
  resizeBrowserView: (paneId: string, bounds: { x: number; y: number; width: number; height: number }) =>
    ipcRenderer.invoke('browser-view:resize', paneId, JSON.stringify(bounds)),

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

  // Dev Update (only exposed when TBOX_DEV_UPDATE=1)
  ...(process.env.TBOX_DEV_UPDATE ? {
    getAppInfo: () => ipcRenderer.invoke('dev:app-info'),
    checkUpdate: (daemonUrl: string) => ipcRenderer.invoke('dev:check-update', daemonUrl),
    applyUpdate: (daemonUrl: string) => ipcRenderer.invoke('dev:apply-update', daemonUrl),
    forceLoadSPA: (mode: 'dev' | 'bundled') => ipcRenderer.invoke('spa:force-load', mode),
    onUpdateProgress: (callback: (step: string) => void) => {
      const handler = (_event: Electron.IpcRendererEvent, step: string) => callback(step)
      ipcRenderer.on('dev:update-progress', handler)
      return () => ipcRenderer.removeListener('dev:update-progress', handler)
    },
  } : {}),
})
