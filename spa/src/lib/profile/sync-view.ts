// spa/src/lib/profile/sync-view.ts — how a `ProfileSyncSnapshot` READS, for the two places that show it: the
// dot on the master's item in the Home menu (ProfileSwitcher) and Settings › Profile. One reading, so the two
// can never disagree about the same snapshot. Pure functions of what `useProfileSync()` hands over.
import type { ExecutorStatus } from './executor'
import type { SectionStatus } from './sync-state'
import { sectionKind, workspaceIdOf } from './projections'
import type { ProfileSyncSnapshot } from './sync-status'

export type SyncDot = 'synced' | 'syncing' | 'locked' | 'problem' | 'unknown'

/** Tailwind classes the app already uses for status dots; no new colour. Beside the reading, so both places paint it alike. */
export const SYNC_DOT_CLASS: Record<SyncDot, string> = {
  synced: 'bg-green-500',
  syncing: 'bg-yellow-500',
  locked: 'bg-amber-500',
  problem: 'bg-red-500',
  unknown: 'bg-gray-500',
}

/**
 * One reading of the whole master; null = no master attached. In a follower window the figures are the
 * leader's (`remote`) and are shown all the same; once `stale` nobody is there to correct them, so the reading
 * stops vouching for them. `problems` is not read: it is a log of the last 50, with nothing saying one is over —
 * a dot driven by it would stay red for ever. What blocks the sync NOW is `blocked`; the log is listed in
 * Settings › Profile.
 */
export function syncDotOf(sync: ProfileSyncSnapshot): SyncDot | null {
  if (sync.master === null) return null
  if (sync.blocked === 'suspended') return 'syncing' // an attach is under way somewhere: transient
  if (sync.blocked !== null || profileIsGone(sync)) return 'problem'
  if (sync.status === null || (sync.remote && sync.stale)) return 'unknown'
  const { profile } = sync.status
  if (profile.startsWith('locked:')) return 'locked'
  if (profile === 'synced') return 'synced'
  if (profile === 'pending') return 'syncing'
  return 'unknown'
}

/**
 * `pending` while Auto-sync is off is NOT "syncing" (P3d-4c F2): with Auto-sync off the executor sends and pulls
 * nothing on its own (`decideSection`: `online` needs `autoSync`) — only *Sync now* does, and only until it is done.
 * The published status cannot tell a section in the middle of such a *Sync now* from one that is simply held (it
 * carries no "in flight"), so this says only what is certain — "waiting, Auto-sync is off" — never "not sent".
 * `autoSync` is the device preference (`useProfileStore`), which every window reads alike. Not while `blocked`
 * (nothing runs then, and the reason is said) nor when the dot does not vouch for the figures (a stale follower).
 */
export function heldByAutoSyncOff(sync: ProfileSyncSnapshot, autoSync: boolean): boolean {
  return !autoSync && sync.blocked === null && syncDotOf(sync) === 'syncing'
}

/** One section of the same reading: `pending`, Auto-sync off, nothing blocking. */
export function sectionHeldByAutoSyncOff(sync: ProfileSyncSnapshot, state: SectionStatus, autoSync: boolean): boolean {
  return !autoSync && state === 'pending' && sync.blocked === null && !profileIsGone(sync)
}

/**
 * The profile is not on the host any more — said by two sources, read alike (P3 plan, P3d-4 decision 2): a 404 on
 * the attachment (`blocked: 'profile-gone'`, start.ts) and the index no longer listing it (the executor's own
 * `profileGone`, which leaves `blocked` null and reads `locked:reset`). Either way nothing syncs until the user
 * chooses another profile.
 */
export function profileIsGone(sync: ProfileSyncSnapshot): boolean {
  return sync.blocked === 'profile-gone' || sync.status?.profileGone === true
}

/**
 * `settings` is neither pulled nor pushed until `workspaces` is up to date (executor.ts, `SETTINGS_GATES`): a
 * scoped entry must not reach the SOT ahead of the `workspaces` that lists its workspace. That gate stays shut
 * while `workspaces` is LOCKED (until the user decides) and while it keeps FAILING — its own requests
 * (`detail.workspaces.failures`) or the index read every section waits for (`indexFailures`) — so a theme change
 * sits on this device. Answers why, when `settings` has something to send (`pending`, a push in flight included):
 * `'locked'` (the one the user can act on; it wins when both hold) · `'failing'` · null = not waiting, or only for
 * a moment (a `workspaces` that is merely pending opens the gate by itself: saying "waiting" then would cry wolf on
 * every edit).
 */
export function settingsWaitForWorkspaces(status: ExecutorStatus | null): 'locked' | 'failing' | null {
  // The profile gone: the executor has stopped for good (no pump, no reindex) and keeps its last sections — a
  // "waiting" read from them would be a wait that never ends (review F2). The 404's status has no sections at all.
  if (status === null || status.profileGone || status.sections.settings !== 'pending') return null
  if (status.sections.workspaces?.startsWith('locked:') ?? false) return 'locked'
  if ((status.detail.workspaces?.failures ?? 0) > 0 || status.indexFailures > 0) return 'failing'
  return null
}

/**
 * A section as a person reads it. `tabs.<workspaceId>` means nothing to a user, so a tabs section carries its
 * workspace's NAME — three-valued on purpose: a string = named · `null` = the master world has no such workspace
 * (the SOT holds tabs of a workspace this device has not taken yet) · `undefined` = the master world cannot be
 * read right now, so nothing is claimed either way. The raw `key` stays for `data-section` and the tooltip.
 */
export type SectionView =
  | { key: string; kind: 'hosts' | 'settings' | 'workspaces' | 'other' }
  | { key: string; kind: 'tabs'; workspace: string | null | undefined }

const FIXED_ORDER = ['hosts', 'settings', 'workspaces'] as const

/**
 * Reading order: hosts, settings, workspaces; the tabs in the order of the workspaces they belong to; tabs of
 * workspaces not there, by key; anything of no known kind last, by key.
 *
 * `masterWorkspaces` MUST BE THE MASTER WORLD'S (`readMasterWorld()`), never the live workspace store: while a
 * slave is on screen the live store holds the SLAVE's workspaces — another list, and an id the two share (a
 * demoted master keeps its ids) would be given the wrong name. `null` = unsettled: nobody can say right now.
 */
export function describeSections(keys: readonly string[], masterWorkspaces: readonly { id: string; name: string }[] | null): SectionView[] {
  const rank = (key: string): [number, number] => {
    const kind = sectionKind(key)
    if (kind === null) return [3, 0]
    if (kind !== 'tabs') return [0, FIXED_ORDER.indexOf(kind)]
    const at = masterWorkspaces?.findIndex((w) => w.id === workspaceIdOf(key)) ?? -1
    return at >= 0 ? [1, at] : [2, 0]
  }
  return [...keys]
    .sort((a, b) => {
      const [ra, rb] = [rank(a), rank(b)]
      return ra[0] - rb[0] || ra[1] - rb[1] || (a < b ? -1 : a > b ? 1 : 0)
    })
    .map((key): SectionView => {
      const kind = sectionKind(key)
      if (kind === null) return { key, kind: 'other' }
      if (kind !== 'tabs') return { key, kind }
      if (masterWorkspaces === null) return { key, kind, workspace: undefined }
      return { key, kind, workspace: masterWorkspaces.find((w) => w.id === workspaceIdOf(key))?.name ?? null }
    })
}
