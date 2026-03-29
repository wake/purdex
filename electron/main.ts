import { app, BrowserWindow, ipcMain, Menu, Notification, protocol, net } from 'electron'
import { join } from 'path'
import { WindowManager } from './window-manager'
import { BrowserViewManager } from './browser-view-manager'
import { createTray } from './tray'
import { getAppInfo, checkUpdate, applyUpdate } from './updater'
import { getDefaultKeybindings, buildMenuTemplate } from './keybindings'

// Register custom protocol before app is ready (Electron requirement).
// 'app://' replaces 'file://' for bundled SPA, enabling standard CORS behavior.
protocol.registerSchemesAsPrivileged([{
  scheme: 'app',
  privileges: { standard: true, secure: true, supportFetchAPI: true },
}])

const windowManager = new WindowManager()
const browserViewManager = new BrowserViewManager()
windowManager.setOnWindowClosed((win) => browserViewManager.cleanupForWindow(win))
let metricsInterval: ReturnType<typeof setInterval> | null = null
let updateInProgress = false

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

  // Notifications
  const recentBroadcasts = new Set<number>()
  ipcMain.handle('notification:show', (_event, optsJson: string) => {
    const opts = JSON.parse(optsJson) as {
      title: string; body: string; sessionCode: string; eventName: string; broadcastTs: number
    }
    // Dedup: same broadcast received by multiple windows
    if (recentBroadcasts.has(opts.broadcastTs)) return
    recentBroadcasts.add(opts.broadcastTs)
    setTimeout(() => recentBroadcasts.delete(opts.broadcastTs), 5000)

    const notification = new Notification({ title: opts.title, body: opts.body })
    notification.on('click', () => {
      // Broadcast to all renderers — SPA decides which one has the tab
      // The SPA will call focusMyWindow IPC when it handles the click
      for (const win of windowManager.getAllWindows()) {
        if (!win.isDestroyed()) {
          win.webContents.send('notification:clicked', { sessionCode: opts.sessionCode })
        }
      }
    })
    notification.show()
  })

  // SPA requests its window to be focused (after handling notification click)
  ipcMain.on('notification:focus-window', (event) => {
    const win = BrowserWindow.fromWebContents(event.sender)
    if (win && !win.isDestroyed()) {
      win.show()
      win.focus()
    }
  })

  // SPA Reload (re-detect dev server, not just location.reload)
  ipcMain.handle('spa:reload', (event) => {
    windowManager.reloadSPA(event.sender)
  })

  // SPA Force Load (skip detection, load specific mode)
  ipcMain.handle('spa:force-load', async (event, mode: 'dev' | 'bundled') => {
    try {
      return await windowManager.forceLoadSPA(event.sender, mode)
    } catch (err) {
      throw String(err instanceof Error ? err.message : err)
    }
  })

  // Dev Update
  ipcMain.handle('dev:app-info', () => getAppInfo())
  ipcMain.handle('dev:check-update', (_event, daemonUrl: string) => checkUpdate(daemonUrl))
  ipcMain.handle('dev:apply-update', async (event, daemonUrl: string) => {
    if (updateInProgress) throw 'Update already in progress'
    updateInProgress = true
    const win = BrowserWindow.fromWebContents(event.sender)
    try {
      return await applyUpdate(daemonUrl, (step) => {
        if (win && !win.isDestroyed()) {
          win.webContents.send('dev:update-progress', step)
        }
      })
    } catch (err) {
      updateInProgress = false
      // Error objects lose their message across contextBridge serialization.
      // Re-throw as a plain string so the renderer gets a useful message.
      throw String(err instanceof Error ? err.message : err)
    }
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
  // Serve bundled renderer files via app:// protocol
  const rendererRoot = join(__dirname, '../renderer')
  protocol.handle('app', (req) => {
    let pathname = new URL(req.url).pathname
    if (pathname === '/') pathname = '/index.html'
    const resolved = join(rendererRoot, pathname)
    if (!resolved.startsWith(rendererRoot)) {
      return new Response('Forbidden', { status: 403 })
    }
    return net.fetch('file://' + resolved)
  })

  registerIpcHandlers()
  createTray(windowManager)

  const keybindings = getDefaultKeybindings()
  const menuTemplate = buildMenuTemplate(
    keybindings,
    (action) => {
      const focused = BrowserWindow.getFocusedWindow()
      if (focused && !focused.isDestroyed()) {
        focused.webContents.send('shortcut:execute', { action })
      }
    },
    { 'new-window': () => windowManager.createWindow() },
  )
  Menu.setApplicationMenu(Menu.buildFromTemplate(menuTemplate))

  windowManager.createWindow()

  startMetricsPolling()

  // Dev: background update check on startup
  if (getAppInfo().devUpdateEnabled) {
    const daemonUrl = 'http://100.64.0.2:7860'
    checkUpdate(daemonUrl).then((remote) => {
      if (remote.building) return // build in progress, SPA will poll
      const local = getAppInfo()
      if (remote.electronHash !== local.electronHash || remote.spaHash !== local.spaHash) {
        for (const win of windowManager.getAllWindows()) {
          if (!win.isDestroyed()) win.webContents.send('dev:update-available', remote)
        }
      }
    }).catch(() => { /* silent — daemon may not be reachable */ })
  }

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
