import { app, BrowserWindow, ipcMain, Menu, Notification, protocol, net } from 'electron'
import { join, resolve } from 'path'
import { WindowManager } from './window-manager'
import { BrowserViewManager } from './browser-view-manager'
import { MiniWindowManager } from './mini-browser-window'
import { registerBrowserViewIpc } from './browser-view-ipc'
import { registerFsIpc } from './fs-ipc'
import { createTray } from './tray'
import { getAppInfo, checkUpdate, applyUpdate, streamCheck } from './updater'
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
windowManager.setOnWindowClosed((win) => {
  browserViewManager.cleanupForWindow(win)
  try {
    readyWebContentsIds.delete(win.webContents.id)
  } catch {
    // webContents already destroyed — id no longer relevant, ignore.
  }
})
let metricsInterval: ReturnType<typeof setInterval> | null = null
let updateInProgress = false

// ── OS protocol handler (purdex://) ─────────────────────────────────────────
// Deeplink format: purdex://execution/<execution_id>[?host=<hint>]
// M0: observe-only navigation. main parses the URL and broadcasts to renderers
// (reusing the notification-click pipeline template); the SPA resolver (P.12)
// decides which window has the tab and focuses it, or falls back to a detail
// page. This handler never writes stdin — deeplink is navigation only.
const DEEPLINK_SCHEME = 'purdex'
const DEEPLINK_CHANNEL = 'deeplink:navigate'

interface ExecutionDeeplink {
  executionId: string
  host?: string
}

// Renderers that have signalled `spa:ready` and can receive a broadcast now.
// A deeplink arriving before any SPA is ready (cold start) is buffered and
// flushed to the first renderer that becomes ready.
const readyWebContentsIds = new Set<number>()
let pendingDeeplinks: ExecutionDeeplink[] = []
// macOS `open-url` and the initial argv deeplink can arrive before the app is
// ready — BrowserWindow cannot be created yet, so raw URLs wait here.
const preReadyDeeplinkUrls: string[] = []
let appReady = false

function parseDeeplink(rawUrl: string): ExecutionDeeplink | null {
  let url: URL
  try {
    url = new URL(rawUrl)
  } catch {
    return null
  }
  if (url.protocol !== `${DEEPLINK_SCHEME}:`) return null
  // purdex://execution/<id> → hostname = 'execution', pathname = '/<id>'
  if (url.hostname !== 'execution') return null
  const executionId = decodeURIComponent(url.pathname.replace(/^\/+/, '').split('/')[0] ?? '')
  if (!executionId) return null
  const host = url.searchParams.get('host')
  return host ? { executionId, host } : { executionId }
}

function findDeeplinkArg(argv: readonly string[]): string | null {
  return argv.find((a) => a.startsWith(`${DEEPLINK_SCHEME}://`)) ?? null
}

// Send to every ready renderer; buffer if none are ready yet (cold start).
function broadcastDeeplink(dl: ExecutionDeeplink): void {
  let delivered = false
  for (const win of windowManager.getAllWindows()) {
    if (!win.isDestroyed() && readyWebContentsIds.has(win.webContents.id)) {
      win.webContents.send(DEEPLINK_CHANNEL, dl)
      delivered = true
    }
  }
  if (!delivered) pendingDeeplinks.push(dl)
}

// Entry point for a deeplink once the app is ready: wake/focus a window, parse,
// and broadcast. Malformed URLs are logged and dropped.
function handleDeeplink(rawUrl: string): void {
  const dl = parseDeeplink(rawUrl)
  if (!dl) {
    console.warn('[deeplink] ignored malformed url:', rawUrl)
    return
  }
  windowManager.showOrCreate()
  broadcastDeeplink(dl)
}

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

  // Browser View — delegated to browser-view-ipc.ts
  registerBrowserViewIpc(browserViewManager, miniWindowManager)

  // Filesystem — delegated to fs-ipc.ts
  registerFsIpc()

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

  // Deeplink readiness — track which renderers can receive a broadcast and
  // flush any deeplink that arrived before an SPA was ready (cold start).
  // This is an additional `spa:ready` listener alongside the per-window ones
  // in WindowManager; both fire independently.
  ipcMain.on('spa:ready', (event) => {
    readyWebContentsIds.add(event.sender.id)
    if (pendingDeeplinks.length > 0) {
      const drain = pendingDeeplinks
      pendingDeeplinks = []
      for (const dl of drain) event.sender.send(DEEPLINK_CHANNEL, dl)
    }
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

  // Dev Update — gated by PDX_DEV_MODE === '1', matching the daemon
  // (internal/module/dev/module.go) and preload (electron/preload.ts).
  if (process.env.PDX_DEV_MODE === '1') {
    ipcMain.handle('dev:app-info', () => getAppInfo())
    ipcMain.handle('dev:check-update', (_event, daemonUrl: string, token?: string) => checkUpdate(daemonUrl, token))

    // Only one active stream at a time — a new request aborts the previous one.
    let activeStream: AbortController | null = null
    ipcMain.handle('dev:stream-check', async (event, daemonUrl: string, token: string | undefined, channel: string) => {
      activeStream?.abort()
      const controller = new AbortController()
      activeStream = controller
      const win = BrowserWindow.fromWebContents(event.sender)
      try {
        await streamCheck(daemonUrl, token, (ev) => {
          if (win && !win.isDestroyed()) {
            win.webContents.send(channel, ev)
          }
        }, controller.signal)
      } catch (err) {
        // AbortError on manual stop is expected — surface everything else.
        const msg = err instanceof Error ? err.message : String(err)
        if ((err as { name?: string })?.name !== 'AbortError' && win && !win.isDestroyed()) {
          win.webContents.send(channel, { type: 'error', error: msg })
        }
      } finally {
        if (activeStream === controller) activeStream = null
      }
    })
    ipcMain.on('dev:stream-check-stop', () => {
      activeStream?.abort()
    })

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
}

function startMetricsPolling(): void {
  metricsInterval = setInterval(() => {
    const metrics = browserViewManager.getMetrics()
    for (const win of windowManager.getAllWindows()) {
      if (!win.isDestroyed()) win.webContents.send('metrics:update', metrics)
    }
  }, 30_000) // 30 seconds
}

// Register purdex:// as the OS default handler for this app. In a packaged app
// a bare call suffices; when run via `electron .` (dev) the launcher path and
// the app entry argv must be passed so the OS invokes the right binary.
if (process.defaultApp) {
  if (process.argv.length >= 2) {
    app.setAsDefaultProtocolClient(DEEPLINK_SCHEME, process.execPath, [resolve(process.argv[1])])
  }
} else {
  app.setAsDefaultProtocolClient(DEEPLINK_SCHEME)
}

// Single-instance lock: a deeplink launched while the app is already running
// spawns a second process. On Windows/Linux the OS passes the URL via that
// process's argv; we forward it to the primary via `second-instance` and quit
// the secondary. Without the lock a duplicate app window would open per click.
const gotInstanceLock = app.requestSingleInstanceLock()
if (!gotInstanceLock) {
  app.quit()
} else {
  app.on('second-instance', (_event, argv) => {
    // Windows/Linux warm deeplink arrives in the second instance's argv.
    windowManager.showOrCreate()
    const url = findDeeplinkArg(argv)
    if (url) handleDeeplink(url)
  })

  // macOS delivers deeplinks via `open-url`, which can fire before the app is
  // ready. Buffer pre-ready URLs; process them once whenReady resolves.
  app.on('open-url', (event, url) => {
    event.preventDefault()
    if (appReady) handleDeeplink(url)
    else preReadyDeeplinkUrls.push(url)
  })

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

    // Cold start: process any deeplink that launched the app.
    appReady = true
    // Windows/Linux: the initial deeplink is in the launch argv.
    const argvDeeplink = findDeeplinkArg(process.argv)
    if (argvDeeplink) handleDeeplink(argvDeeplink)
    // macOS: replay any open-url events buffered before ready.
    const buffered = preReadyDeeplinkUrls.splice(0)
    for (const url of buffered) handleDeeplink(url)
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
}
