import { Tray, Menu, nativeImage, app } from 'electron'
import { existsSync } from 'node:fs'
import type { WindowManager } from './window-manager'
import { resolveTrayIconPath } from './tray-icon'

let tray: Tray | null = null

// Idempotent: returns the existing tray if one is already up. Returns null
// (and creates nothing) when the icon file cannot be found — an empty
// nativeImage would otherwise show up as an invisible blank slot in the
// menu bar.
export function createTray(windowManager: WindowManager): Tray | null {
  if (tray) return tray

  const iconPath = resolveTrayIconPath(__dirname, existsSync)
  if (!iconPath) {
    console.warn('[tray] icon not found, tray not created')
    return null
  }

  // trayTemplate.png is 16px black+alpha with a 32px @2x sibling — already the
  // right size for a macOS template image, so no resize here.
  const icon = nativeImage.createFromPath(iconPath)
  icon.setTemplateImage(true)

  tray = new Tray(icon)
  tray.setToolTip('Purdex')
  tray.setContextMenu(
    Menu.buildFromTemplate([
      // TODO: i18n — main process has no access to SPA i18n store; wire via IPC when needed
      { label: 'Show Window', click: () => windowManager.showOrCreate() },
      { type: 'separator' },
      { label: 'Quit Purdex', click: () => app.quit() },
    ]),
  )

  tray.on('click', () => windowManager.showOrCreate())

  return tray
}

export function destroyTray(): void {
  tray?.destroy()
  tray = null
}

export function isTrayVisible(): boolean {
  return tray !== null
}

export function setTrayVisible(visible: boolean, windowManager: WindowManager): void {
  if (visible) createTray(windowManager)
  else destroyTray()
}
