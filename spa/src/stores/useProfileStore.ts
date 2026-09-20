// spa/src/stores/useProfileStore.ts — Profile Sync's control plane.
//
// Which profile this client is attached to (`masterHostId` + `masterProfileId`)
// whether it syncs on its own (`autoSync`), and which side wins the FIRST
// reconciliation after an attach (`pendingDirection`). Nothing else.
//
// WHY ONLY THESE. This store holds exactly what EVERY window of this
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
// WHY `pendingDirection` IS HERE and not in the driver's memory (spec §4.9,
// decision 10). A client that attaches has never agreed with the SOT on
// anything, so every section where both sides hold something is a conflict the
// state machine cannot settle — only the user can, and they did, once, when
// they attached: "push" (this machine overwrites the SOT) or "pull" (the SOT
// overwrites this machine). That answer has to outlive the call that gave it:
// the first reconciliation may span a reload (the host is offline right now),
// and it may be carried out by ANOTHER window — the one holding the lease. So it
// is persisted and synced like the master itself, and the leader clears it
// (`clearPendingDirection`) once everything has settled. It is never cleared by
// a timeout: see `lib/profile/executor.ts`, THE FIRST RECONCILIATION.
//
// WHY `attachGeneration`. Every attach is a NEW first reconciliation, an attach
// to the master already set included (the user asked again, perhaps in the
// other direction; the bases of the previous one are discarded by
// `attachMaster`). But "the same master, again" changes neither id, so a driver
// that is already running — in this window, or the leader in another — could
// not tell. The counter is what it watches: it goes up by one with every
// accepted `setMaster` and never down; its value means nothing, only that it
// moved.
//
// WHY `masterEndpoint` ("<ip>:<port>" of the master host AT ATTACH). The section
// bases, the attachment, a schema lock — all of it belongs to ONE daemon, and the
// api layer resolves a host's address from the host store on every request. If
// the user re-points the master host in place, the next request would carry the
// old daemon's CAS bases to whatever answers at the new address. The start layer
// blocks the driver when the two differ; the home address is stored HERE, not
// remembered by the driver, because a reload while blocked would otherwise take
// the edited address for the original and unblock itself. Null = unknown: only a
// master attached before this field existed; the start layer adopts the current
// address once (`adoptMasterEndpoint`) — trusting it the first time is the best
// that can be done for such an installation, and it guards from then on.
//
// INVARIANTS: `masterHostId` and `masterProfileId` are both null or both
// non-null; `pendingDirection` and `masterEndpoint` are null whenever there is no master. `setMaster`
// is the only way in and validates all three; the persist `merge` re-establishes
// both for whatever storage hands back.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

/** The daemon's profile id: "p_" + 12 lowercase hex chars. */
const PROFILE_ID_PATTERN = /^p_[0-9a-f]{12}$/

export type SyncDirection = 'push' | 'pull'

interface ProfileControl {
  masterHostId: string | null
  masterProfileId: string | null
  /** Non-null from an attach until the first reconciliation has settled. */
  pendingDirection: SyncDirection | null
  /** `"<ip>:<port>"` of the master host when it was attached; null = unknown (see the header) or no master. */
  masterEndpoint: string | null
  /** +1 with every accepted `setMaster`. A change with the same master = attach was called again. */
  attachGeneration: number
  /** Sync without being asked. Default on. */
  autoSync: boolean
}

export interface ProfileState extends ProfileControl {
  /** Attach. Both ids must be non-empty strings, `profileId` a daemon profile
   *  id, `direction` one of the two and `endpoint` a non-empty string; otherwise nothing changes and the answer
   *  is `false`. Attaching again to the same master starts a new first
   *  reconciliation in the direction given. */
  setMaster: (hostId: string, profileId: string, direction: SyncDirection, endpoint: string) => boolean
  /** Fills in an UNKNOWN endpoint of the current master; anything else is ignored. Not an attach. */
  adoptMasterEndpoint: (endpoint: string) => void
  /** Detach. `autoSync` is a preference and survives; the direction does not. */
  clearMaster: () => void
  /** The first reconciliation has settled: conflicts go to the user from now on. */
  clearPendingDirection: () => void
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

export function isSyncDirection(v: unknown): v is SyncDirection {
  return v === 'push' || v === 'pull'
}

function isEndpoint(v: unknown): v is string {
  return typeof v === 'string' && v !== ''
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
    pendingDirection: attached && isSyncDirection(p.pendingDirection) ? p.pendingDirection : null,
    masterEndpoint: attached && isEndpoint(p.masterEndpoint) ? p.masterEndpoint : null,
    attachGeneration: Number.isSafeInteger(p.attachGeneration) && (p.attachGeneration as number) >= 0 ? (p.attachGeneration as number) : 0,
    autoSync: typeof p.autoSync === 'boolean' ? p.autoSync : true,
  }
}

export const useProfileStore = create<ProfileState>()(
  persist(
    (set) => ({
      masterHostId: null,
      masterProfileId: null,
      pendingDirection: null,
      masterEndpoint: null,
      attachGeneration: 0,
      autoSync: true,
      setMaster: (hostId, profileId, direction, endpoint) => {
        if (!isMasterPair(hostId, profileId) || !isSyncDirection(direction) || !isEndpoint(endpoint)) return false
        set((s) => ({ masterHostId: hostId, masterProfileId: profileId, pendingDirection: direction, masterEndpoint: endpoint, attachGeneration: s.attachGeneration + 1 }))
        return true
      },
      adoptMasterEndpoint: (endpoint) =>
        set((s) => (s.masterHostId === null || s.masterEndpoint !== null || !isEndpoint(endpoint) ? s : { masterEndpoint: endpoint })),
      clearMaster: () => set({ masterHostId: null, masterProfileId: null, pendingDirection: null, masterEndpoint: null }),
      clearPendingDirection: () => set({ pendingDirection: null }),
      setAutoSync: (value) => set({ autoSync: value === true }),
    }),
    {
      name: STORAGE_KEYS.PROFILE,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({
        masterHostId: state.masterHostId,
        masterProfileId: state.masterProfileId,
        pendingDirection: state.pendingDirection,
        masterEndpoint: state.masterEndpoint,
        attachGeneration: state.attachGeneration,
        autoSync: state.autoSync,
      }),
      // Only the six sanitised fields ever come out of storage: persisted
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
