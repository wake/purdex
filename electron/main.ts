import { app, BrowserWindow, dialog, ipcMain, Menu, Notification, protocol, net } from 'electron'
import { join } from 'path'
import { WindowManager } from './window-manager'
import { BrowserViewManager } from './browser-view-manager'
import { MiniWindowManager } from './mini-browser-window'
import { registerBrowserViewIpc } from './browser-view-ipc'
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
const miniWindowManager = new MiniWindowManager(browserViewManager)
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
  ipcMain.handle('window:tear-off-workspace', (_event, payload: string) => {
    windowManager.handleTearOffWorkspace(payload)
  })
  ipcMain.handle('window:merge-workspace', (_event, payload: string, targetWindowId: string) => {
    const ok = windowManager.handleMergeWorkspace(payload, targetWindowId)
    if (!ok) throw 'Target window not found'
  })
  ipcMain.handle('window:get-all', (event) => {
    return windowManager.getAll(event.sender)
  })
  ipcMain.handle('window:close', async (event) => {
    const win = BrowserWindow.fromWebContents(event.sender)
    if (!win || win.isDestroyed()) return
    const { response } = await dialog.showMessageBox(win, {
      type: 'question',
      buttons: ['Close', 'Cancel'],
      defaultId: 0,
      cancelId: 1,
      message: 'Close this window?',
    })
    if (response === 0) win.close()
  })

  // Browser View — delegated to browser-view-ipc.ts
  registerBrowserViewIpc(browserViewManager, miniWindowManager)

  // Memory Monitor
  ipcMain.handle('metrics:get', () => {
    return browserViewManager.getMetrics()
  })

  // Notifications — prevent GC from collecting Notification objects before
  // the user clicks them. Without this Set, the local `notification` variable
  // goes out of scope after show(), V8 can GC the JS wrapper, and the click
  // handler is silently lost. Electron's C++ layer does NOT use SelfKeepAlive
  // for Notification (unlike Tray), so userland must hold the reference.
  // No TTL — notification frequency is low and each object is ~hundreds of
  // bytes; a slow leak from missing close events is negligible.
  const recentBroadcasts = new Set<number>()
  const activeNotifications = new Set<Notification>()
  ipcMain.handle('notification:show', (_event, optsJson: string) => {
    const opts = JSON.parse(optsJson) as {
      title: string; body: string; sessionCode: string; eventName: string; broadcastTs: number
      action?: { kind: string; hostId: string; sessionCode?: string }
    }
    // Dedup: same broadcast received by multiple windows
    if (recentBroadcasts.has(opts.broadcastTs)) return
    recentBroadcasts.add(opts.broadcastTs)
    setTimeout(() => recentBroadcasts.delete(opts.broadcastTs), 5000)

    const notification = new Notification({ title: opts.title, body: opts.body })
    activeNotifications.add(notification)
    const release = () => { activeNotifications.delete(notification) }
    notification.on('click', () => {
      release()
      // Broadcast to all renderers — SPA decides which one has the tab
      // The SPA will call focusMyWindow IPC when it handles the click
      const payload: { sessionCode: string; action?: { kind: string; hostId: string; sessionCode?: string } } = {
        sessionCode: opts.sessionCode,
      }
      if (opts.action) payload.action = opts.action
      for (const win of windowManager.getAllWindows()) {
        if (!win.isDestroyed()) {
          win.webContents.send('notification:clicked', payload)
        }
      }
    })
    notification.on('close', release)
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
  ipcMain.handle('dev:check-update', (_event, daemonUrl: string, token?: string) => checkUpdate(daemonUrl, token))
  ipcMain.handle('dev:apply-update', async (event, daemonUrl: string, token?: string) => {
    if (updateInProgress) throw 'Update already in progress'
    updateInProgress = true
    const win = BrowserWindow.fromWebContents(event.sender)
    try {
      return await applyUpdate(daemonUrl, (step) => {
        if (win && !win.isDestroyed()) {
          win.webContents.send('dev:update-progress', step)
        }
      }, token)
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
  miniWindowManager.closeAll()
  browserViewManager.destroyAll()
})
