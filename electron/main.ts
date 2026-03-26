import { app, BrowserWindow, ipcMain } from 'electron'
import { WindowManager } from './window-manager'
import { BrowserViewManager } from './browser-view-manager'
import { createTray } from './tray'

const windowManager = new WindowManager()
const browserViewManager = new BrowserViewManager()
windowManager.setOnWindowClosed((win) => browserViewManager.cleanupForWindow(win))
let metricsInterval: ReturnType<typeof setInterval> | null = null

function registerIpcHandlers(): void {
  // Window Management
  ipcMain.handle('window:tear-off', (_event, tabJson: string) => {
    windowManager.handleTearOff(tabJson)
  })
  ipcMain.handle('window:merge', (_event, tabJson: string, targetWindowId: string) => {
    windowManager.handleMerge(tabJson, targetWindowId)
  })
  ipcMain.handle('window:get-all', () => {
    return windowManager.getAll()
  })

  // Browser View
  ipcMain.handle('browser-view:open', (event, url: string, paneId: string) => {
    const win = BrowserWindow.fromWebContents(event.sender)
    if (win) browserViewManager.open(win, url, paneId)
  })
  ipcMain.handle('browser-view:close', (_event, paneId: string) => {
    browserViewManager.background(paneId)
  })
  ipcMain.handle('browser-view:navigate', (_event, paneId: string, url: string) => {
    browserViewManager.navigate(paneId, url)
  })
  ipcMain.handle('browser-view:resize', (_event, paneId: string, boundsJson: string) => {
    try {
      const raw = JSON.parse(boundsJson)
      browserViewManager.resize(paneId, {
        x: Math.round(Number(raw.x)) || 0,
        y: Math.round(Number(raw.y)) || 0,
        width: Math.max(1, Math.round(Number(raw.width)) || 1),
        height: Math.max(1, Math.round(Number(raw.height)) || 1),
      })
    } catch { /* ignore malformed bounds JSON */ }
  })

  // Memory Monitor
  ipcMain.handle('metrics:get', () => {
    return browserViewManager.getMetrics()
  })
}

function startMetricsPolling(): void {
  metricsInterval = setInterval(() => {
    const metrics = browserViewManager.getMetrics()
    for (const win of windowManager.getAllWindows()) {
      if (!win.isDestroyed()) win.webContents.send('metrics:update', metrics)
    }
  }, 30_000) // 30 seconds
}

app.whenReady().then(() => {
  registerIpcHandlers()
  createTray(windowManager)

  windowManager.createWindow()

  startMetricsPolling()

  app.on('activate', () => {
    windowManager.showOrCreate()
  })
})

// macOS: close window ≠ quit app
app.on('window-all-closed', () => {
  // no-op on macOS — tray keeps running
})

app.on('before-quit', () => {
  if (metricsInterval) clearInterval(metricsInterval)
  browserViewManager.destroyAll()
})
