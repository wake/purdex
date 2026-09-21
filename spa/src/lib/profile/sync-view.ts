// spa/src/lib/profile/sync-view.ts — how a `ProfileSyncSnapshot` READS, for the two places that show it: the
// dot on the master's item in the Home menu (ProfileSwitcher) and Settings › Profile. One reading, so the two
// can never disagree about the same snapshot. Pure functions of what `useProfileSync()` hands over.
import type { ExecutorStatus } from './executor'
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
  if (sync.blocked !== null) return 'problem'
  if (sync.status === null || (sync.remote && sync.stale)) return 'unknown'
  const { profile } = sync.status
  if (profile.startsWith('locked:')) return 'locked'
  if (profile === 'synced') return 'synced'
  if (profile === 'pending') return 'syncing'
  return 'unknown'
}

/**
 * `settings` is neither pulled nor pushed until `workspaces` is up to date (executor.ts, `SETTINGS_GATES`): a
 * scoped entry must not reach the SOT ahead of the `workspaces` that lists its workspace. While `workspaces` is
 * LOCKED that gate stays shut until the user decides — so a theme change sits on this device with no visible
 * reason. True exactly then: `settings` has something to send (`pending`) and `workspaces` is `locked:*`.
 *
 * WHAT THIS CANNOT SEE. The executor's gate is `upToDate('workspaces')`, which is also shut while `workspaces`
 * keeps FAILING (in back-off) — and the published status does not say so: a failing section reads `pending`,
 * exactly like one that will be through in a moment, and `problems` is a log with no "over". Saying "waiting"
 * for every `pending` would cry wolf on each edit; so the failing case is NOT said until the executor publishes
 * it (a per-section `backingOff` / `failing` in `ExecutorStatus` would do).
 */
export function settingsWaitForWorkspaces(status: ExecutorStatus | null): boolean {
  if (status === null) return false
  return status.sections.settings === 'pending' && (status.sections.workspaces?.startsWith('locked:') ?? false)
}
