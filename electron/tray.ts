import { Tray, Menu, nativeImage, app } from 'electron'
import { join } from 'path'
import type { WindowManager } from './window-manager'

let tray: Tray | null = null

export function createTray(windowManager: WindowManager): Tray {
  // macOS Template image (auto dark/light) — nativeImage does not support SVG
  const iconPath = join(__dirname, '../../spa/public/icons/icon-192.png')
  const icon = nativeImage.createFromPath(iconPath).resize({ width: 18, height: 18 })
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
