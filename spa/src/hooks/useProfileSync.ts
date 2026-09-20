// spa/src/hooks/useProfileSync.ts — Profile Sync's status, for a component in ANY
// window (P3 plan Task 2). In the leader it is this window's own view; in a
// follower it is what the leader published (`remote: true`, and `stale: true`
// when nobody is there to correct it). Without a master: `master: null`, and
// subscribing costs a Set entry — no storage, no listener, no timer (start.ts,
// THE IRON RULE).
//   To act on it: `requestSyncNow()` and `requestResolve(section, keep,
// snapshot.status?.conflicts[section] ?? null)` from lib/profile/start — pass the
// pair that was ON SCREEN, that is what makes the resolve safe.
import { useSyncExternalStore } from 'react'
import { profileSyncSnapshot, subscribeProfileSync } from '../lib/profile/start'
import type { ProfileSyncSnapshot } from '../lib/profile/start'

export function useProfileSync(): ProfileSyncSnapshot {
  // `profileSyncSnapshot` is cached: its identity changes only when its content did.
  return useSyncExternalStore(subscribeProfileSync, profileSyncSnapshot, profileSyncSnapshot)
}
