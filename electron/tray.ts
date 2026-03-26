import { Tray, Menu, nativeImage, app } from 'electron'
import { join } from 'path'
import type { WindowManager } from './window-manager'

let tray: Tray | null = null

export function createTray(windowManager: WindowManager): Tray {
  // macOS Template image (auto dark/light)
  const iconPath = join(__dirname, '../../spa/public/favicon.svg')
  const icon = nativeImage.createFromPath(iconPath).resize({ width: 18, height: 18 })
  icon.setTemplateImage(true)

  tray = new Tray(icon)
  tray.setToolTip('tmux-box')
  tray.setContextMenu(
    Menu.buildFromTemplate([
      { label: 'Show Window', click: () => windowManager.showOrCreate() },
      { type: 'separator' },
      { label: 'Quit', click: () => app.quit() },
    ]),
  )

  tray.on('click', () => windowManager.showOrCreate())

  return tray
}

export function destroyTray(): void {
  tray?.destroy()
  tray = null
}
