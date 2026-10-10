// spa/src/lib/team/moving-tabs.ts — tabs this window is deliberately deleting because they move to another window
// (tear-off, merge; #2140). team-tab-lifecycle reads "a lead's tab left the tab store" as "the lead was closed"; a move
// is not that, so the paths that move a tab say so first. The mark is short-lived and used up by the first pass that
// sees the tab gone, so a later real close of a tab with the same id is never taken for a move.

/** Long enough for the subscriber's microtask to run after the delete; short enough that a stale mark cannot linger. */
export const MOVING_TTL_MS = 2000

const moving = new Map<string, number>() // tab id -> expiry (ms since epoch)

/** Call BEFORE the tab store deletes the tabs. */
export function markTabsMoving(tabIds: readonly string[]): void {
  const expires = Date.now() + MOVING_TTL_MS
  for (const id of tabIds) moving.set(id, expires)
}

/** Whether `tabId` is marked and the mark is still alive. Does not clear it. */
export function isTabMoving(tabId: string): boolean {
  const expires = moving.get(tabId)
  if (expires === undefined) return false
  if (expires <= Date.now()) {
    moving.delete(tabId)
    return false
  }
  return true
}

/** The mark is spent: the move has been seen. */
export function clearTabMoving(tabId: string): void {
  moving.delete(tabId)
}

/** Test seam. */
export function resetMovingTabs(): void {
  moving.clear()
}
