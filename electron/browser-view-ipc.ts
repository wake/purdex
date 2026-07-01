import { ipcMain, BrowserWindow, shell } from 'electron'
import { isAllowedUrl } from './browser-view-manager'
import type { BrowserViewManager } from './browser-view-manager'
import type { MiniWindowManager } from './mini-browser-window'

// Open a URL in the OS default browser, scheme-guarded and never rejecting.
// shell.openExternal can reject (no OS handler, platform failure); we catch so
// neither the fire-and-forget link-click path nor the ipcMain.handle path can
// surface an unhandled promise rejection in the main process.
async function openAllowedExternalUrl(url: string): Promise<void> {
  if (!isAllowedUrl(url)) return
  try {
    await shell.openExternal(url)
  } catch (err) {
    console.error('[browser-view] shell.openExternal failed:', err)
  }
}

export function registerBrowserViewIpc(
  manager: BrowserViewManager,
  miniWindowManager: MiniWindowManager,
): void {
  // --- Existing handlers (moved from main.ts) ---

  ipcMain.handle('browser-view:open', (event, url: string, paneId: string) => {
    const win = BrowserWindow.fromWebContents(event.sender)
    if (win) manager.open(win, url, paneId)
  })

  ipcMain.handle('browser-view:close', (_event, paneId: string) => {
    manager.background(paneId)
  })

  ipcMain.handle('browser-view:navigate', (_event, paneId: string, url: string) => {
    manager.navigate(paneId, url)
  })

  ipcMain.handle('browser-view:resize', (_event, paneId: string, boundsJson: string) => {
    try {
      const raw = JSON.parse(boundsJson)
      manager.resize(paneId, {
        x: Math.round(Number(raw.x)) || 0,
        y: Math.round(Number(raw.y)) || 0,
        width: Math.max(1, Math.round(Number(raw.width)) || 1),
        height: Math.max(1, Math.round(Number(raw.height)) || 1),
      })
    } catch { /* ignore malformed bounds JSON */ }
  })

  // --- New navigation handlers ---

  ipcMain.handle('browser-view:go-back', (_event, paneId: string) => {
    manager.goBack(paneId)
  })

  ipcMain.handle('browser-view:go-forward', (_event, paneId: string) => {
    manager.goForward(paneId)
  })

  ipcMain.handle('browser-view:reload', (_event, paneId: string) => {
    manager.reload(paneId)
  })

  ipcMain.handle('browser-view:stop', (_event, paneId: string) => {
    manager.stop(paneId)
  })

  ipcMain.handle('browser-view:print', (_event, paneId: string) => {
    manager.print(paneId)
  })

  ipcMain.handle('browser-view:destroy', (_event, paneId: string) => {
    manager.destroy(paneId)
  })

  // --- Mini browser window ---

  ipcMain.handle('browser-view:open-mini-window', (event, url: string) => {
    const parentWin = BrowserWindow.fromWebContents(event.sender)
    if (parentWin) miniWindowManager.open(parentWin, url)
  })

  ipcMain.handle('browser-view:move-to-tab', (_event, paneId: string) => {
    miniWindowManager.moveToTab(paneId)
  })

  // Open a URL in the OS default browser. Scheme-guarded (http/https only) so a
  // stray token can never hand file:// or a custom scheme to the OS handler.
  // Always resolves (failures are caught + logged), so the renderer's invoke
  // never rejects.
  ipcMain.handle('shell:open-external', (_event, url: string) => openAllowedExternalUrl(url))

  // --- State request (SPA loaded after view was created) ---

  ipcMain.handle('browser-view:request-state', (_event, paneId: string) => {
    manager.pushStateNow(paneId)
  })

  // --- Link click from WebContentsView preload ---

  ipcMain.on('browser-view:link-click', (event, data: { url: string; shiftKey: boolean; targetBlank: boolean }) => {
    const entry = manager.getEntryByWebContents(event.sender)
    if (!entry) return
    if (!isAllowedUrl(data.url)) return

    if (data.shiftKey) {
      // Shift+Click inside a browser pane → OS default browser (consistent with
      // terminal-link shift+click). Plain click still opens an in-app tab.
      // Fire-and-forget; the helper catches so there is no unhandled rejection.
      void openAllowedExternalUrl(data.url)
    } else {
      // Notify parent window SPA to open new browser tab
      try {
        entry.window.webContents.send('browser-view:open-in-tab', data.url)
      } catch { /* window may be closed */ }
    }
  })
}
