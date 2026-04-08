import { WebContentsView, BrowserWindow, app, type WebContents } from 'electron'
import { join } from 'node:path'

const ALLOWED_SCHEMES = new Set(['http:', 'https:'])

export function isAllowedUrl(url: string): boolean {
  try {
    return ALLOWED_SCHEMES.has(new URL(url).protocol)
  } catch {
    return false
  }
}

interface Bounds {
  x: number
  y: number
  width: number
  height: number
}

interface ViewEntry {
  view: WebContentsView
  paneId: string
  url: string
  window: BrowserWindow
  state: 'active' | 'background'
  lastActiveAt: number
  cleanupListeners?: () => void
}

interface Snapshot {
  url: string
  paneId: string
}

interface TabMetrics {
  paneId: string
  kind: string
  memoryKB: number
  cpuPercent: number
  state: 'active' | 'background' | 'discarded'
}

// Default settings — can be overridden via IPC from SPA settings
const DEFAULTS = {
  idleTimeoutMs: 5 * 60 * 1000,   // 5 minutes
  memoryLimitMB: 512,
  maxBackground: 3,
}

export class BrowserViewManager {
  private views = new Map<string, ViewEntry>()
  private snapshots = new Map<string, Snapshot>()
  private timers = new Map<string, ReturnType<typeof setTimeout>>()
  private checkInterval: ReturnType<typeof setInterval> | null = null

  constructor() {
    // Periodic memory check
    this.checkInterval = setInterval(() => this.checkMemoryLimit(), 30_000)
  }

  open(win: BrowserWindow, url: string, paneId: string): void {
    // If already exists, just activate
    if (this.views.has(paneId)) {
      this.activate(paneId)
      return
    }

    // Check for snapshot (discarded view) — use saved URL instead of prop
    const snapshot = this.snapshots.get(paneId)
    const loadUrl = snapshot ? snapshot.url : url
    this.snapshots.delete(paneId)

    if (!isAllowedUrl(loadUrl)) return

    const view = new WebContentsView({
      webPreferences: {
        contextIsolation: true,
        sandbox: true,
        backgroundThrottling: false, // Active — no throttling
        preload: join(__dirname, '../preload/browserViewPreload.js'),
      },
    })

    // Security: restrict navigation to http/https
    view.webContents.setWindowOpenHandler(() => ({ action: 'deny' }))
    view.webContents.on('will-navigate', (event, navUrl) => {
      if (!isAllowedUrl(navUrl)) event.preventDefault()
    })

    win.contentView.addChildView(view)
    view.webContents.loadURL(loadUrl)

    const entry: ViewEntry = {
      view,
      paneId,
      url: loadUrl,
      window: win,
      state: 'active',
      lastActiveAt: Date.now(),
    }
    this.views.set(paneId, entry)

    // Push state updates to the SPA in the owning window
    const wc = view.webContents
    const pushState = () => {
      try {
        if (entry.window.isDestroyed() || wc.isDestroyed()) return
        entry.window.webContents.send('browser-view:state-update', paneId, {
          url: wc.getURL(),
          title: wc.getTitle(),
          canGoBack: wc.canGoBack(),
          canGoForward: wc.canGoForward(),
          isLoading: wc.isLoading(),
        })
      } catch { /* window or webContents may be closed */ }
    }
    wc.on('did-navigate', pushState)
    wc.on('did-navigate-in-page', pushState)
    wc.on('did-start-loading', pushState)
    wc.on('did-stop-loading', pushState)
    wc.on('page-title-updated', pushState)

    entry.cleanupListeners = () => {
      wc.off('did-navigate', pushState)
      wc.off('did-navigate-in-page', pushState)
      wc.off('did-start-loading', pushState)
      wc.off('did-stop-loading', pushState)
      wc.off('page-title-updated', pushState)
    }

    this.clearTimer(paneId)
  }

  /** Move view to background (not destroyed — idle timer will discard later). */
  background(paneId: string): void {
    this.deactivate(paneId)
  }

  navigate(paneId: string, url: string): void {
    const entry = this.views.get(paneId)
    if (entry && isAllowedUrl(url)) {
      entry.url = url
      entry.view.webContents.loadURL(url)
    }
  }

  resize(paneId: string, bounds: Bounds): void {
    const entry = this.views.get(paneId)
    if (entry) {
      entry.view.setBounds(bounds)
    }
  }

  goBack(paneId: string): void {
    const entry = this.views.get(paneId)
    if (entry?.view.webContents.canGoBack()) {
      entry.view.webContents.goBack()
    }
  }

  goForward(paneId: string): void {
    const entry = this.views.get(paneId)
    if (entry?.view.webContents.canGoForward()) {
      entry.view.webContents.goForward()
    }
  }

  reload(paneId: string): void {
    const entry = this.views.get(paneId)
    entry?.view.webContents.reload()
  }

  stop(paneId: string): void {
    const entry = this.views.get(paneId)
    entry?.view.webContents.stop()
  }

  print(paneId: string): void {
    const entry = this.views.get(paneId)
    entry?.view.webContents.print()
  }

  destroy(paneId: string): void {
    const entry = this.views.get(paneId)
    if (!entry) return
    this.clearTimer(paneId)
    entry.cleanupListeners?.()
    try {
      entry.window.contentView.removeChildView(entry.view)
    } catch { /* already removed */ }
    try {
      entry.view.webContents.close()
    } catch { /* already closed */ }
    this.views.delete(paneId)
  }

  getEntryByWebContents(wc: WebContents): ViewEntry | undefined {
    for (const entry of this.views.values()) {
      if (entry.view.webContents === wc) return entry
    }
    return undefined
  }

  getCurrentState(paneId: string): { url: string; title: string } | undefined {
    const entry = this.views.get(paneId)
    if (!entry || entry.view.webContents.isDestroyed()) return undefined
    return {
      url: entry.view.webContents.getURL(),
      title: entry.view.webContents.getTitle(),
    }
  }

  private activate(paneId: string): void {
    const entry = this.views.get(paneId)
    if (!entry) {
      // Restore from snapshot
      const snapshot = this.snapshots.get(paneId)
      if (snapshot) {
        // Need a window — caller should provide. For now, use first available.
        return
      }
      return
    }

    entry.state = 'active'
    entry.lastActiveAt = Date.now()
    entry.view.webContents.backgroundThrottling = false
    this.clearTimer(paneId)
    // Bounds will be set by SPA ResizeObserver via resize()
  }

  private deactivate(paneId: string): void {
    const entry = this.views.get(paneId)
    if (!entry) return

    entry.state = 'background'
    entry.view.webContents.backgroundThrottling = true
    // Move off-screen
    entry.view.setBounds({ x: -10000, y: -10000, width: 1, height: 1 })

    // Start idle timer
    this.startIdleTimer(paneId)

    // Check max background count
    this.enforceMaxBackground()
  }

  private startIdleTimer(paneId: string): void {
    this.clearTimer(paneId)
    this.timers.set(paneId, setTimeout(() => {
      this.discard(paneId)
    }, DEFAULTS.idleTimeoutMs))
  }

  private clearTimer(paneId: string): void {
    const timer = this.timers.get(paneId)
    if (timer) {
      clearTimeout(timer)
      this.timers.delete(paneId)
    }
  }

  private discard(paneId: string): void {
    const entry = this.views.get(paneId)
    if (!entry) return

    entry.cleanupListeners?.()

    if (entry.window.isDestroyed()) {
      // Window already closed — just clean up our references
      entry.view.webContents.close()
      this.views.delete(paneId)
      this.clearTimer(paneId)
      return
    }

    // Save snapshot
    this.snapshots.set(paneId, {
      url: entry.view.webContents.getURL() || entry.url,
      paneId,
    })

    // Destroy
    entry.window.contentView.removeChildView(entry.view)
    entry.view.webContents.close()
    this.views.delete(paneId)
    this.clearTimer(paneId)
  }

  private enforceMaxBackground(): void {
    const bgEntries = Array.from(this.views.values())
      .filter((e) => e.state === 'background')
      .sort((a, b) => a.lastActiveAt - b.lastActiveAt)

    while (bgEntries.length > DEFAULTS.maxBackground) {
      const oldest = bgEntries.shift()!
      try {
        this.discard(oldest.paneId)
      } catch { /* view or window may have been destroyed concurrently */ }
    }
  }

  private checkMemoryLimit(): void {
    const metrics = app.getAppMetrics()
    let totalViewMemoryKB = 0

    for (const entry of this.views.values()) {
      if (entry.view.webContents.isDestroyed()) continue
      const pid = entry.view.webContents.getOSProcessId()
      const metric = metrics.find((m) => m.pid === pid)
      if (metric?.memory) {
        totalViewMemoryKB += metric.memory.workingSetSize ?? 0
      }
    }

    const totalMB = totalViewMemoryKB / 1024
    if (totalMB > DEFAULTS.memoryLimitMB) {
      // Discard oldest background view
      const bgEntries = Array.from(this.views.values())
        .filter((e) => e.state === 'background')
        .sort((a, b) => a.lastActiveAt - b.lastActiveAt)

      if (bgEntries.length > 0) {
        this.discard(bgEntries[0].paneId)
      }
    }
  }

  getMetrics(): TabMetrics[] {
    const appMetrics = app.getAppMetrics()
    const result: TabMetrics[] = []

    for (const entry of this.views.values()) {
      if (entry.view.webContents.isDestroyed()) continue
      const pid = entry.view.webContents.getOSProcessId()
      const metric = appMetrics.find((m) => m.pid === pid)
      result.push({
        paneId: entry.paneId,
        kind: 'browser',
        memoryKB: metric?.memory?.workingSetSize ?? 0,
        cpuPercent: metric?.cpu?.percentCPUUsage ?? 0,
        state: entry.state,
      })
    }

    // Add discarded snapshots
    for (const snapshot of this.snapshots.values()) {
      result.push({
        paneId: snapshot.paneId,
        kind: 'browser',
        memoryKB: 0,
        cpuPercent: 0,
        state: 'discarded',
      })
    }

    return result
  }

  cleanupForWindow(win: BrowserWindow): void {
    for (const [paneId, entry] of this.views) {
      if (entry.window === win) {
        this.clearTimer(paneId)
        if (!entry.view.webContents.isDestroyed()) {
          entry.view.webContents.close()
        }
        this.views.delete(paneId)
      }
    }
  }

  destroyAll(): void {
    if (this.checkInterval) {
      clearInterval(this.checkInterval)
      this.checkInterval = null
    }
    for (const paneId of [...this.views.keys()]) {
      const entry = this.views.get(paneId)!
      entry.window.contentView.removeChildView(entry.view)
      entry.view.webContents.close()
    }
    this.views.clear()
    this.snapshots.clear()
    for (const timer of this.timers.values()) clearTimeout(timer)
    this.timers.clear()
  }
}
