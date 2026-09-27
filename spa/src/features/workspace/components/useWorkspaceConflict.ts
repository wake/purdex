// spa/src/features/workspace/components/useWorkspaceConflict.ts — what a workspace row's sync-conflict icon and panel
// read (WorkspaceConflict.tsx has the why), and the three moments the row closes its own `useConflictPanelStore` entry.
import { useEffect, type RefObject } from 'react'
import { useMasterScreen } from '../../../hooks/useMasterOnScreen'
import { useProfileSync } from '../../../hooks/useProfileSync'
import { tabsLockOf } from '../../../lib/profile/conflict-view'
import type { SectionLock } from '../../../lib/profile/executor'
import type { ProfileSyncSnapshot } from '../../../lib/profile/start'
import { useConflictPanelStore } from '../../../stores/useConflictPanelStore'

export interface WorkspaceConflictState {
  /** The lock to show — null when there is none, or when this row does not stand for a master workspace right now. */
  lock: SectionLock | null
  /** This row's panel is the open one, and there is a lock to show in it. */
  open: boolean
  sync: ProfileSyncSnapshot
}

/** `buttonRef`: the icon, scrolled into view when the panel opens (it may have been opened from the Home popover). */
export function useWorkspaceConflict(workspaceId: string, buttonRef: RefObject<HTMLElement | null>): WorkspaceConflictState {
  const sync = useProfileSync()
  const screen = useMasterScreen()
  const rawLock = tabsLockOf(sync, workspaceId)
  const lock = screen === 'master' ? rawLock : null
  const entryOpen = useConflictPanelStore((s) => s.openWsId === workspaceId)
  const open = entryOpen && lock !== null

  // Gone for good: the lock lifted, or a slave ACTUALLY on screen. Merely unsettled is neither (review I5): hidden,
  // the entry kept, back with the world.
  const gone = entryOpen && (rawLock === null || screen === 'slave')
  useEffect(() => {
    if (gone) useConflictPanelStore.getState().closeFor(workspaceId)
  }, [gone, workspaceId])

  // Unmounted (the workspace deleted, the bar gone narrow): its entry must not wait for a row that is not there (M8).
  useEffect(() => () => useConflictPanelStore.getState().closeFor(workspaceId), [workspaceId])

  // Optional call: jsdom has no `scrollIntoView`.
  useEffect(() => {
    if (open) buttonRef.current?.scrollIntoView?.({ block: 'nearest' })
  }, [open, buttonRef])

  return { lock, open, sync }
}
