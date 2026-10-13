// electron/badge-ipc.ts — wires the Dock badge (badge.ts, app-badge spec §3.2) to the renderers and to the OS.
// The SPA of every window sends its report on `badge:set`; main keeps the last one per `webContents.id` and sets
// `app.setBadgeCount` to the union count. `setBadgeCount` is the Dock badge on macOS (and a no-op where unsupported).
import { app, BrowserWindow, ipcMain } from 'electron'
import { attachBadgeSender, BadgeCenter, parseBadgeReport } from './badge'

export function registerBadgeIpc(): BadgeCenter {
  const center = new BadgeCenter((n) => { app.setBadgeCount(n) })
  center.clear() // a crashed run may have left its badge on the Dock

  ipcMain.on('badge:set', (event, raw: unknown) => {
    const report = parseBadgeReport(raw)
    if (!report) {
      console.warn('[badge] ignoring a malformed report')
      return
    }
    const sender = event.sender
    attachBadgeSender(center, sender)
    // `destroyed` already covers a window closing; the window's own `closed` is the same fact from its side.
    BrowserWindow.fromWebContents(sender)?.once('closed', () => center.drop(sender.id))
    center.set(sender.id, report)
  })
  return center
}
