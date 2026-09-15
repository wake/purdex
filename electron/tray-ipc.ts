// Menu bar tray IPC. The main process owns the preference (app-prefs.json)
// because it has to decide before any renderer exists whether to create the
// tray; renderers read/write it only through these channels.
import { BrowserWindow, ipcMain } from 'electron'
import { loadAppPrefs, saveAppPrefs } from './app-prefs'
import { createTray, destroyTray, isTrayVisible } from './tray'
import { applyTrayVisibility, type TrayVisibilityDeps } from './tray-visibility'
import type { WindowManager } from './window-manager'

export function registerTrayIpc(opts: { prefsPath: string; windowManager: WindowManager }): void {
  const { prefsPath, windowManager } = opts
  const deps: TrayVisibilityDeps = {
    isVisible: isTrayVisible,
    create: () => createTray(windowManager) !== null,
    destroy: destroyTray,
    loadPrefs: () => loadAppPrefs(prefsPath),
    savePrefs: (p) => saveAppPrefs(prefsPath, p),
  }

  ipcMain.handle('tray:get-visible', () => isTrayVisible())

  ipcMain.handle('tray:set-visible', (_event, visible: boolean) => {
    const applied = applyTrayVisibility(visible === true, deps)
    // Every window's Settings page mirrors the same main-process state, so
    // tell all of them — including the caller, which is harmless.
    for (const w of BrowserWindow.getAllWindows()) {
      if (!w.isDestroyed()) w.webContents.send('tray:visibility-changed', applied)
    }
    return applied
  })
}
