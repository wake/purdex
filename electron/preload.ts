import { contextBridge, ipcRenderer } from 'electron'

contextBridge.exposeInMainWorld('electronAPI', {
  // Window Management
  tearOffTab: (tabJson: string) => ipcRenderer.invoke('window:tear-off', tabJson),
  mergeTab: (tabJson: string, targetWindowId: string) =>
    ipcRenderer.invoke('window:merge', tabJson, targetWindowId),
  getWindows: () => ipcRenderer.invoke('window:get-all'),
  onTabReceived: (callback: (tabJson: string) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, tabJson: string) => callback(tabJson)
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

  // Memory Monitor
  getProcessMetrics: () => ipcRenderer.invoke('metrics:get'),
  onMetricsUpdate: (callback: (metrics: unknown[]) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, metrics: unknown[]) => callback(metrics)
    ipcRenderer.on('metrics:update', handler)
    return () => ipcRenderer.removeListener('metrics:update', handler)
  },
})
