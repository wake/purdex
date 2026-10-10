// electron/notification-router.ts — where a notification click goes (#1919). A broadcast event reaches every window, and the
// first window to ask for the notification (main.ts dedups the rest) is the one the person sees it come from; its click goes
// to that window only, so the other windows do not focus themselves or restore their own minimized approval dialog (spec
// U22 (b): a minimized dialog is per window). When that window is gone the click falls back to every window, as before.

export interface ClickWindow {
  /** The renderer's `webContents.id`. */
  id: number
  isDestroyed(): boolean
  send(channel: string, payload: unknown): void
}

export const NOTIFICATION_CLICKED = 'notification:clicked'

/** Sends `payload` to the window with `ownerId` when it is alive, else to every live window. Returns how many got it. */
export function deliverNotificationClick(ownerId: number | undefined, windows: readonly ClickWindow[], payload: unknown): number {
  const live = windows.filter((w) => !w.isDestroyed())
  const owner = ownerId === undefined ? undefined : live.find((w) => w.id === ownerId)
  const targets = owner ? [owner] : live
  for (const w of targets) w.send(NOTIFICATION_CLICKED, payload)
  return targets.length
}
