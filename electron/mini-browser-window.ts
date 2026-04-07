import { BrowserWindow } from 'electron'
import { join } from 'node:path'
import type { BrowserViewManager } from './browser-view-manager'

interface MiniWindowEntry {
  window: BrowserWindow
  paneId: string
  parentWindow: BrowserWindow
}

const DEV_SERVER = 'http://100.64.0.2:5174'

export class MiniWindowManager {
  private entries = new Map<string, MiniWindowEntry>()
  private nextId = 0

  constructor(
    private viewManager: BrowserViewManager,
    // WindowManager type not imported to avoid circular — only need DEV_SERVER pattern
  ) {}

  open(parentWindow: BrowserWindow, url: string): void {
    const paneId = `mini-${++this.nextId}`

    const win = new BrowserWindow({
      width: 800,
      height: 600,
      titleBarStyle: 'hiddenInset',
      trafficLightPosition: { x: 12, y: 12 },
      webPreferences: {
        contextIsolation: true,
        sandbox: true,
        nodeIntegration: false,
        preload: join(__dirname, '../preload/index.js'),
      },
    })

    this.entries.set(paneId, { window: win, paneId, parentWindow })

    // Load mini browser SPA entry
    const query = `?paneId=${encodeURIComponent(paneId)}`
    fetch(DEV_SERVER, { signal: AbortSignal.timeout(500) })
      .then(() => win.loadURL(`${DEV_SERVER}/mini-browser.html${query}`))
      .catch(() => win.loadURL(`app://./mini-browser.html${query}`))

    // Once SPA is ready, open the WebContentsView
    win.webContents.once('did-finish-load', () => {
      if (win.isDestroyed()) return
      this.viewManager.open(win, url, paneId)
    })

    // Cleanup on window close
    win.on('closed', () => {
      this.viewManager.destroy(paneId)
      this.entries.delete(paneId)
    })
  }

  moveToTab(paneId: string): void {
    const entry = this.entries.get(paneId)
    if (!entry) return

    const state = this.viewManager.getCurrentState(paneId)
    const url = state?.url
    if (!url) return  // View not yet loaded — nothing to move

    // Notify parent window SPA to open new tab
    try {
      if (!entry.parentWindow.isDestroyed()) {
        entry.parentWindow.webContents.send('browser-view:open-in-tab', url)
      }
    } catch { /* parent window may be closed */ }

    // Close mini window (triggers 'closed' event → cleanup)
    if (!entry.window.isDestroyed()) {
      entry.window.close()
    }
  }

  closeAll(): void {
    for (const entry of this.entries.values()) {
      if (!entry.window.isDestroyed()) {
        entry.window.close()
      }
    }
  }
}
