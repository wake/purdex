// electron/notification-router.ts — where a notification click goes (#1919). A broadcast event reaches every window, and the
// first window to ask for the notification (main.ts dedups the rest) is the one the person sees it come from.
//
// An APPROVAL click goes to that window only: the others would otherwise focus themselves and restore their own minimized
// approval dialog (spec U22 (b): a minimized dialog is per window). Every other click (`open-session`, `open-host`) is still
// broadcast: which window handles an `open-session` click depends on which window holds the session's tab, and the window
// that happened to ask first may hold none — only the SPA of each window knows. When the owner is gone an approval click
// falls back to every window too.

export interface ClickWindow {
  /** The renderer's `webContents.id`. */
  id: number
  isDestroyed(): boolean
  send(channel: string, payload: unknown): void
}

export const NOTIFICATION_CLICKED = 'notification:clicked'

/** The click kinds that belong to the window that showed the notification. */
export const OWNER_ONLY_ACTIONS: ReadonlySet<string> = new Set(['open-approval'])

/**
 * Sends `payload` to the live window with `ownerId` when `actionKind` is owner-only and that window is alive; otherwise to
 * every live window. Returns how many got it.
 */
export function deliverNotificationClick(
  ownerId: number | undefined,
  actionKind: string | undefined,
  windows: readonly ClickWindow[],
  payload: unknown,
): number {
  const live = windows.filter((w) => !w.isDestroyed())
  const ownerOnly = actionKind !== undefined && OWNER_ONLY_ACTIONS.has(actionKind)
  const owner = ownerOnly && ownerId !== undefined ? live.find((w) => w.id === ownerId) : undefined
  const targets = owner ? [owner] : live
  for (const w of targets) w.send(NOTIFICATION_CLICKED, payload)
  return targets.length
}
