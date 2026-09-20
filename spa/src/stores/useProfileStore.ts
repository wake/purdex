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
// the edited address for the original and unblock itself.
//   A MASTER WITHOUT AN ENDPOINT IS NO MASTER (fail closed; `merge` and
// `selectMaster` both). Such a record can only come from before this field
// existed — dev builds, never a shipped one — and "adopt the address the host
// has today" would legitimise bases of an unknown daemon against whatever the
// user has since pointed the host at. There is nothing worth migrating: the user
// attaches again, which clears the bases anyway.
//
// WHY `suspension` = { token, until }. `attachMaster` has to wait for the daemon
// (the attachment PUT, up to 15 s) before it can clear the bases and start the
// new reconciliation. A driver left running meanwhile — in this window, or the
// leader in another — could push a dirty section in that gap, and a `pull` would
// then find a SOT that this machine has just overwritten: the direction the user
// asked for, reversed, for good. So the attach first tells EVERY window to stand
// still: no driver, no request, master and bases untouched.
//   `until` — a TIME and not a flag, because the window that set it may die
// before it can lift it (closed or reloaded mid-attach), and a flag would leave
// every window of this client suspended for ever; an expired one is simply none.
//   `token` — an OWNER, because windows have separate attach queues and two
// attaches can overlap: A suspends, B suspends, A's PUT fails (or succeeds) — and
// A must not wake the drivers under B. A newer `suspend` replaces the older; only
// the token that set the current one lifts it (`resume(token)`, a successful
// `setMaster(…, token)`). So: WHILE ANY ATTACH IS STILL IN PROGRESS, THE DRIVERS
// STAND STILL. Which master wins two overlapping attaches is last-writer-wins, as
// everywhere in this store. `clearMaster` lifts everything: detach means stop.
//
// INVARIANTS: `masterHostId` and `masterProfileId` are both null or both
// non-null, and non-null only together with `masterEndpoint`; `pendingDirection` and `suspension` are null whenever there is no master. `setMaster`
// is the only way in and validates all three; the persist `merge` re-establishes
// both for whatever storage hands back.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

/** The daemon's profile id: "p_" + 12 lowercase hex chars. */
const PROFILE_ID_PATTERN = /^p_[0-9a-f]{12}$/

export type SyncDirection = 'push' | 'pull'

export interface Suspension {
  token: string
  until: number
}

interface ProfileControl {
  masterHostId: string | null
  masterProfileId: string | null
  /** Non-null from an attach until the first reconciliation has settled. */
  pendingDirection: SyncDirection | null
  /** `"<ip>:<port>"` of the master host when it was attached; null exactly when there is no master. */
  masterEndpoint: string | null
  /** While `now < suspension.until` (epoch ms) no window runs a driver: an attach — `token`'s — is in progress. */
  suspension: Suspension | null
  /** +1 with every accepted `setMaster`. A change with the same master = attach was called again. */
  attachGeneration: number
  /** Sync without being asked. Default on. */
  autoSync: boolean
}

export interface ProfileState extends ProfileControl {
  /** Attach. Both ids must be non-empty strings, `profileId` a daemon profile
   *  id, `direction` one of the two and `endpoint` a non-empty string; otherwise nothing changes and the answer
   *  is `false`. Attaching again to the same master starts a new first
   *  reconciliation in the direction given. `token`: the suspension this attach set is
   *  lifted with it; one set by ANOTHER attach (a newer one, still in progress) stays. */
  setMaster: (hostId: string, profileId: string, direction: SyncDirection, endpoint: string, token?: string) => boolean
  /** Every driver stands still until `until` (epoch ms). Replaces any suspension there is. Ignored without a master. */
  suspend: (token: string, until: number) => void
  /** Lifts the suspension `token` set; any other token changes NOTHING (same state reference, no persist). */
  resume: (token: string) => void
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

function isSuspension(token: unknown, until: unknown): boolean {
  return typeof token === 'string' && token !== '' && typeof until === 'number' && Number.isFinite(until)
}

function sanitiseSuspension(v: unknown): Suspension | null {
  if (typeof v !== 'object' || v === null || Array.isArray(v)) return null
  const { token, until } = v as Record<string, unknown>
  return isSuspension(token, until) ? { token: token as string, until: until as number } : null
}

/** Whatever storage held → a record that satisfies the invariant. Half a
 *  master, a wrong type or a malformed profile id all mean "detached": there is
 *  no safe way to guess the missing half, and a detached client does nothing. */
function sanitiseControl(persisted: unknown): ProfileControl {
  const p = (typeof persisted === 'object' && persisted !== null ? persisted : {}) as Record<string, unknown>
  const attached = isMasterPair(p.masterHostId, p.masterProfileId) && isEndpoint(p.masterEndpoint)
  return {
    masterHostId: attached ? (p.masterHostId as string) : null,
    masterProfileId: attached ? (p.masterProfileId as string) : null,
    pendingDirection: attached && isSyncDirection(p.pendingDirection) ? p.pendingDirection : null,
    masterEndpoint: attached ? (p.masterEndpoint as string) : null,
    suspension: attached ? sanitiseSuspension(p.suspension) : null,
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
      suspension: null,
      attachGeneration: 0,
      autoSync: true,
      setMaster: (hostId, profileId, direction, endpoint, token) => {
        if (!isMasterPair(hostId, profileId) || !isSyncDirection(direction) || !isEndpoint(endpoint)) return false
        set((s) => ({ masterHostId: hostId, masterProfileId: profileId, pendingDirection: direction, masterEndpoint: endpoint, suspension: s.suspension !== null && s.suspension.token === token ? null : s.suspension, attachGeneration: s.attachGeneration + 1 }))
        return true
      },
      suspend: (token, until) => set((s) => (selectMaster(s) === null || !isSuspension(token, until) ? s : { suspension: { token, until } })),
      resume: (token) => set((s) => (s.suspension !== null && s.suspension.token === token ? { suspension: null } : s)),
      clearMaster: () => set({ masterHostId: null, masterProfileId: null, pendingDirection: null, masterEndpoint: null, suspension: null }),
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
        suspension: state.suspension,
        attachGeneration: state.attachGeneration,
        autoSync: state.autoSync,
      }),
      // Only the seven sanitised fields ever come out of storage: persisted
      // junk can neither add a key nor replace an action.
      merge: (persisted, current) => ({ ...current, ...sanitiseControl(persisted) }),
    },
  ),
)

/** The attached master, or null. Null for half a master too, so a caller never
 *  has to re-check the invariant. Returns a fresh object: as a zustand selector
 *  it needs `useShallow`. */
export function selectMaster(
  s: Pick<ProfileState, 'masterHostId' | 'masterProfileId' | 'masterEndpoint'>,
): { hostId: string; profileId: string } | null {
  if (s.masterHostId === null || s.masterProfileId === null || s.masterEndpoint === null) return null
  return { hostId: s.masterHostId, profileId: s.masterProfileId }
}

syncManager.register(STORAGE_KEYS.PROFILE, useProfileStore)
