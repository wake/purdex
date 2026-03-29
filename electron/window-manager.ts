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

  createWindow(opts?: { tabJson?: string; replace?: boolean }): BrowserWindow {
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
    if (opts?.tabJson) {
      const replace = opts.replace ?? false
      const handler = (event: Electron.IpcMainEvent) => {
        if (event.sender === win.webContents) {
          win.webContents.send('tab:received', opts.tabJson, replace)
          ipcMain.removeListener('spa:ready', handler)
        }
      }
      ipcMain.on('spa:ready', handler)
    }

    win.on('closed', () => {
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

  closeWindow(windowId: string): void {
    const win = this.windows.get(windowId)
    if (win && !win.isDestroyed()) {
      win.close()
    }
  }

  getAll(): WindowInfo[] {
    return Array.from(this.windows.entries()).map(([id, win]) => ({
      id,
      title: win.isDestroyed() ? '' : win.getTitle(),
    }))
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
}
