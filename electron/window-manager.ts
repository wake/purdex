import { BrowserWindow, app, ipcMain } from 'electron'
import { join } from 'path'

interface WindowInfo {
  id: string
  title: string
}

export class WindowManager {
  private windows = new Map<string, BrowserWindow>()
  private onWindowClosed?: (win: BrowserWindow) => void

  setOnWindowClosed(cb: (win: BrowserWindow) => void): void {
    this.onWindowClosed = cb
  }

  createWindow(opts?: { tabJson?: string; workspaceJson?: string; replace?: boolean }): BrowserWindow {
    const id = globalThis.crypto.randomUUID().slice(0, 8)
    const win = new BrowserWindow({
      width: 1200,
      height: 800,
      titleBarStyle: 'hiddenInset',
      trafficLightPosition: { x: 12, y: 12 },
      webPreferences: {
        contextIsolation: true,
        sandbox: true,
        nodeIntegration: false,
        preload: join(__dirname, '../preload/index.js'),
      },
    })

    this.windows.set(id, win)

    // Load SPA: try dev server first, fallback to bundled renderer
    this.loadSPA(win)

    // If tab data provided, send after SPA signals ready
    const pendingHandlers: Array<(event: Electron.IpcMainEvent) => void> = []

    if (opts?.tabJson) {
      const replace = opts.replace ?? false
      const handler = (event: Electron.IpcMainEvent) => {
        if (event.sender === win.webContents) {
          win.webContents.send('tab:received', opts.tabJson, replace)
          ipcMain.removeListener('spa:ready', handler)
        }
      }
      pendingHandlers.push(handler)
      ipcMain.on('spa:ready', handler)
    }

    if (opts?.workspaceJson) {
      const replace = opts.replace ?? false
      const handler = (event: Electron.IpcMainEvent) => {
        if (event.sender === win.webContents) {
          win.webContents.send('workspace:received', opts.workspaceJson, replace)
          ipcMain.removeListener('spa:ready', handler)
        }
      }
      pendingHandlers.push(handler)
      ipcMain.on('spa:ready', handler)
    }

    win.webContents.on('render-process-gone', (_event, details) => {
      console.error(`[WindowManager] renderer crashed: ${details.reason}`)
      if (!win.isDestroyed()) this.loadSPA(win)
    })

    win.on('closed', () => {
      for (const h of pendingHandlers) ipcMain.removeListener('spa:ready', h)
      this.onWindowClosed?.(win)
      this.windows.delete(id)
    })

    return win
  }

  private static readonly DEV_SERVER = 'http://100.64.0.2:5174'

  loadSPA(win: BrowserWindow): void {
    fetch(WindowManager.DEV_SERVER, { signal: AbortSignal.timeout(500) })
      .then(() => win.loadURL(WindowManager.DEV_SERVER))
      .catch(() => win.loadURL('app://./index.html'))
  }

  reloadSPA(webContents: Electron.WebContents): void {
    const win = BrowserWindow.fromWebContents(webContents)
    if (win && !win.isDestroyed()) this.loadSPA(win)
  }

  async forceLoadSPA(webContents: Electron.WebContents, mode: 'dev' | 'bundled'): Promise<void> {
    const win = BrowserWindow.fromWebContents(webContents)
    if (!win || win.isDestroyed()) return
    if (mode === 'dev') {
      // Verify dev server is reachable before navigating — loadURL navigates
      // immediately, so a failure would strand the user on an error page.
      // Timeout is longer than loadSPA's 500ms because this is user-initiated.
      const res = await fetch(WindowManager.DEV_SERVER, { signal: AbortSignal.timeout(2000) })
      if (!res.ok) throw new Error(`Dev server responded with ${res.status}`)
      await win.loadURL(WindowManager.DEV_SERVER)
    } else if (mode === 'bundled') {
      await win.loadURL('app://./index.html')
    }
  }

  closeWindow(windowId: string): void {
    const win = this.windows.get(windowId)
    if (win && !win.isDestroyed()) {
      win.close()
    }
  }

  getAll(excludeWebContents?: Electron.WebContents): WindowInfo[] {
    const excludeWin = excludeWebContents ? BrowserWindow.fromWebContents(excludeWebContents) : null
    return Array.from(this.windows.entries())
      .filter(([, win]) => !win.isDestroyed() && win !== excludeWin)
      .map(([id, win]) => ({ id, title: win.getTitle() }))
  }

  getAllWindows(): BrowserWindow[] {
    return Array.from(this.windows.values()).filter((w) => !w.isDestroyed())
  }

  showOrCreate(): void {
    const wins = this.getAllWindows()
    if (wins.length > 0) {
      const win = wins[0]
      if (win.isMinimized()) win.restore()
      win.show()
      win.focus()
    } else {
      this.createWindow()
    }
  }

  handleTearOff(tabJson: string): void {
    this.createWindow({ tabJson, replace: true })
  }

  handleMerge(tabJson: string, targetWindowId: string): void {
    const target = this.windows.get(targetWindowId)
    if (target && !target.isDestroyed()) {
      target.webContents.send('tab:received', tabJson)
      target.show()
      target.focus()
    }
  }

  handleTearOffWorkspace(payload: string): void {
    this.createWindow({ workspaceJson: payload, replace: true })
  }

  handleMergeWorkspace(payload: string, targetWindowId: string): boolean {
    const target = this.windows.get(targetWindowId)
    if (!target || target.isDestroyed()) return false
    target.webContents.send('workspace:received', payload)
    target.show()
    target.focus()
    return true
  }
}
