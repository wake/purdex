// spa/src/lib/profile/conflict-view.ts — the master's locks as the SIDEBAR reads them (sidebar conflict icons spec §2):
// the Home row's icon and popover, the master's item in the Home menu, a workspace row's icon. One reading, so the
// three can never disagree about the same snapshot. Pure functions of what `useProfileSync()` hands over — nothing
// is stored, so an icon goes the moment the executor (or, in a follower, the leader) drops the lock.
//
// WHAT COUNTS: the section locks (`status.locks`: conflict / reset / invalid) and nothing else. The profile-level
// states — `locked:schema`, a gone profile, `blocked` — keep the surfaces they have (Settings › Profile, the dot).
// THE PROFILE GONE → NOTHING: the executor keeps its last locks, but none of them can be resolved any more.
// A STALE follower still reads its locks: a lock is a fact, not a promise (the rule CurrentBlock follows).
import type { SectionLock } from './executor'
import { tabsSectionKey, workspaceIdOf } from './projections'
import { isSyncableWorkspaceId } from './sections'
import type { ProfileSyncSnapshot } from './sync-status'
import { describeSections, profileIsGone, type SectionView } from './sync-view'

export interface LockView {
  key: string
  kind: SectionView['kind']
  /** Only for `tabs.<id>`: the workspace whose tabs are locked. */
  workspaceId: string | null
  status: SectionLock['status']
  /** The section as the section list reads it (the tabs' workspace NAME from the master world, when it could be read). */
  view: SectionView
}

/** The status whose locks can be acted on, or null (no master, no status yet, the profile gone). */
function lockingStatus(sync: ProfileSyncSnapshot) {
  if (sync.master === null || sync.status === null || profileIsGone(sync)) return null
  return sync.status
}

/**
 * Every lock of the master, in `describeSections` order. `masterWorkspaces` MUST be the master world's
 * (`readMasterWorld()`), never the live store — see `describeSections`; null = unsettled, tabs then go by key.
 */
export function locksOf(sync: ProfileSyncSnapshot, masterWorkspaces: readonly { id: string; name: string }[] | null): LockView[] {
  const status = lockingStatus(sync)
  if (status === null) return []
  return describeSections(Object.keys(status.locks), masterWorkspaces).map((view) => ({
    key: view.key,
    kind: view.kind,
    workspaceId: view.kind === 'tabs' ? workspaceIdOf(view.key) : null,
    status: status.locks[view.key].status,
    view,
  }))
}

/**
 * The lock of `tabs.<workspaceId>`, or null. An id that cannot form a section key (a device-local workspace) has
 * none — and is refused BEFORE `tabsSectionKey`, which throws on it: this runs in render.
 */
export function tabsLockOf(sync: ProfileSyncSnapshot, workspaceId: string): SectionLock | null {
  const status = lockingStatus(sync)
  if (status === null || !isSyncableWorkspaceId(workspaceId)) return null
  const key = tabsSectionKey(workspaceId)
  return Object.hasOwn(status.locks, key) ? status.locks[key] : null
}
