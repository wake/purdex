// spa/src/stores/useProfileStore.ts — Profile Sync's control plane.
//
// Which profile this client is attached to (`masterHostId` + `masterProfileId`)
// and whether it syncs on its own (`autoSync`). Nothing else.
//
// WHY ONLY THESE THREE. This store holds exactly what EVERY window of this
// client must agree on, and so it is registered with `syncManager`. If windows
// disagreed, a follower's detach would never reach the leader (which would go
// on pushing to a profile the user left), and a window that was open before the
// attach would never learn there is a lease to queue for.
//
// The per-section working state (base / currentHash / conflict / the payload
// stash) is deliberately NOT here — it lives in `lib/profile/section-store.ts`.
// A cross-window rehydrate is a FULL-STATE REPLACE: had the bases lived in this
// store, any window touching `autoSync` would swap every base under the live
// driver. Only the leader writes section state, so it needs no broadcast.
//
// WHY `masterHostId` IS STORED rather than derived from the dev host / active
// host: those are things the user changes for unrelated reasons. Deriving the
// master from them would silently re-point this client at ANOTHER daemon's
// profile of the same id — or at nothing — the moment the user switched hosts.
// Attaching is an explicit act; so is pointing it somewhere else.
//
// INVARIANT: `masterHostId` and `masterProfileId` are both null or both
// non-null. `setMaster` is the only way in and validates both; the persist
// `merge` re-establishes it for whatever storage hands back.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

/** The daemon's profile id: "p_" + 12 lowercase hex chars. */
const PROFILE_ID_PATTERN = /^p_[0-9a-f]{12}$/

interface ProfileControl {
  masterHostId: string | null
  masterProfileId: string | null
  /** Sync without being asked. Default on. */
  autoSync: boolean
}

export interface ProfileState extends ProfileControl {
  /** Attach. Both must be non-empty strings and `profileId` a daemon profile
   *  id; otherwise nothing changes and the answer is `false`. */
  setMaster: (hostId: string, profileId: string) => boolean
  /** Detach. `autoSync` is a preference and survives. */
  clearMaster: () => void
  setAutoSync: (value: boolean) => void
}

/** What `setMaster` accepts. Exported for `lib/profile/start.ts`, which must know
 *  BEFORE it writes an attachment that the master it is about to set will be taken. */
export function isMasterPair(hostId: unknown, profileId: unknown): boolean {
  return (
    typeof hostId === 'string' &&
    hostId !== '' &&
    typeof profileId === 'string' &&
    PROFILE_ID_PATTERN.test(profileId)
  )
}

/** Whatever storage held → a record that satisfies the invariant. Half a
 *  master, a wrong type or a malformed profile id all mean "detached": there is
 *  no safe way to guess the missing half, and a detached client does nothing. */
function sanitiseControl(persisted: unknown): ProfileControl {
  const p = (typeof persisted === 'object' && persisted !== null ? persisted : {}) as Record<string, unknown>
  const attached = isMasterPair(p.masterHostId, p.masterProfileId)
  return {
    masterHostId: attached ? (p.masterHostId as string) : null,
    masterProfileId: attached ? (p.masterProfileId as string) : null,
    autoSync: typeof p.autoSync === 'boolean' ? p.autoSync : true,
  }
}

export const useProfileStore = create<ProfileState>()(
  persist(
    (set) => ({
      masterHostId: null,
      masterProfileId: null,
      autoSync: true,
      setMaster: (hostId, profileId) => {
        if (!isMasterPair(hostId, profileId)) return false
        set({ masterHostId: hostId, masterProfileId: profileId })
        return true
      },
      clearMaster: () => set({ masterHostId: null, masterProfileId: null }),
      setAutoSync: (value) => set({ autoSync: value === true }),
    }),
    {
      name: STORAGE_KEYS.PROFILE,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({
        masterHostId: state.masterHostId,
        masterProfileId: state.masterProfileId,
        autoSync: state.autoSync,
      }),
      // Only the three sanitised fields ever come out of storage: persisted
      // junk can neither add a key nor replace an action.
      merge: (persisted, current) => ({ ...current, ...sanitiseControl(persisted) }),
    },
  ),
)

/** The attached master, or null. Null for half a master too, so a caller never
 *  has to re-check the invariant. Returns a fresh object: as a zustand selector
 *  it needs `useShallow`. */
export function selectMaster(
  s: Pick<ProfileState, 'masterHostId' | 'masterProfileId'>,
): { hostId: string; profileId: string } | null {
  if (s.masterHostId === null || s.masterProfileId === null) return null
  return { hostId: s.masterHostId, profileId: s.masterProfileId }
}

syncManager.register(STORAGE_KEYS.PROFILE, useProfileStore)
