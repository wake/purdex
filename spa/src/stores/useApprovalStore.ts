// spa/src/stores/useApprovalStore.ts — the open approval requests this app shows (lead-team spec §6.3), one
// entry per (host, request id), fed by the `approval.request` WS branch and the daemon's OnSubscribe snapshot.
// Never persisted: it is what is on screen right now, and every new connection replays the open set.
//
// `decidedHere` marks the requests this app sent a decide for, so the `closed` that follows our own 200 does
// not toast "已由 … 核准" at the person who clicked. `queued` holds a decision clicked while the host was not
// connected (spec §9.4); the snapshot that arrives on reconnect either re-adds the request (the decision is
// sent then, once) or shows it gone (toast). `closedIds` are per-host tombstones: a `closed` that arrives
// before its `opened` (a socket that connected between the two) must not let the late `opened` revive a
// request the daemon already closed; the snapshot is authoritative and clears them. The store is pure data;
// the sending lives in lib/team.
//
// `minimized` (U22 (b)) is this window's 縮小: the dialog is hidden behind a corner pill. Per renderer and never
// persisted, so a reload shows the dialog again. It is temporary: whatever leaves nothing open clears it, so the next
// request opens the dialog; a new request (`applyOpened`) never does — only a click restores.
import { create } from 'zustand'
import type { Approval, Grant } from '../lib/team/types'

/** Tombstones kept per host; past this the oldest is dropped (a request id is a UUID, 256 is hours of closes). */
export const TOMBSTONES_PER_HOST = 256

export type Decision = 'approve' | 'deny'

export interface ApprovalEntry {
  hostId: string
  approval: Approval
}

export interface QueuedDecision {
  hostId: string
  approval: Approval
  decision: Decision
  grant?: Grant
  /** 「這個 session 不再詢問」 was ticked: the session id to pause (POST /api/relay/self off) before the decision. */
  pauseSession?: string
}

/** NUL-joined, as the pane-keyed dialogs do: a host id or a request id may contain any printable separator. */
export const approvalKey = (hostId: string, id: string): string => `${hostId}\u0000${id}`

export interface ApprovalStoreState {
  entries: Record<string, ApprovalEntry>
  queued: Record<string, QueuedDecision>
  decidedHere: Record<string, true>
  /** Per host, the ids closed in this socket generation, oldest first, at most TOMBSTONES_PER_HOST. */
  closedIds: Record<string, string[]>
  /**
   * Per host, how many times `forgetHost` has run (#1978): a generation counter. A decision in flight records it before
   * it sends and gives up on its answer when it moved — the answer belongs to a daemon the id no longer names. Kept
   * across `reset` (a counter that went back to 0 could match an old caller's record).
   */
  hostEpoch: Record<string, number>
  /** The dialog is minimized to the corner pill (U22 (b)); only ever true while `entries` is non-empty. */
  minimized: boolean
  /** `true` takes effect only while a request is open. */
  setMinimized: (v: boolean) => void
  /** Replace the host's whole open set from a snapshot and clear its tombstones; returns the ids held before that are not in it. */
  applySnapshot: (hostId: string, approvals: Approval[]) => string[]
  /** Add an opened request; false when it was already held, is not open, or was closed before (tombstoned). */
  applyOpened: (hostId: string, approval: Approval) => boolean
  /** Remove a request on any close and tombstone its id (absent or not). Tells whether the decision was ours, elsewhere, or the request unknown. */
  applyClosed: (hostId: string, approval: Approval) => 'absent' | 'ours' | 'elsewhere'
  markDecidedHere: (hostId: string, id: string) => void
  unmarkDecidedHere: (hostId: string, id: string) => void
  queueDecision: (hostId: string, approval: Approval, decision: Decision, grant?: Grant, pauseSession?: string) => void
  /** Remove and return the host's queued decisions (each is sent at most once). */
  takeQueued: (hostId: string) => QueuedDecision[]
  /**
   * Drop everything held for a host: its open requests, the decisions queued or marked for them, its tombstones (#1978).
   * For a host that was removed or re-pointed, where what was held belongs to a daemon the id no longer names. Silent:
   * nothing was decided, so no toast; the request is simply gone, and a dialog showing it moves on like after any close.
   * Also bumps `hostEpoch[hostId]`, which voids the answers of decisions already in flight.
   */
  forgetHost: (hostId: string) => void
  reset: () => void
}

function without<T>(record: Record<string, T>, key: string): Record<string, T> {
  if (!Object.hasOwn(record, key)) return record
  const next = { ...record }
  delete next[key]
  return next
}

/** `closedIds` with `id` appended for `hostId` (once), the oldest dropped past the cap. */
function tombstone(closedIds: Record<string, string[]>, hostId: string, id: string): Record<string, string[]> {
  const had = closedIds[hostId] ?? []
  if (had.includes(id)) return closedIds
  const next = had.length >= TOMBSTONES_PER_HOST ? had.slice(had.length - TOMBSTONES_PER_HOST + 1) : had
  return { ...closedIds, [hostId]: [...next, id] }
}

/** The `minimized` patch for an update that leaves `entries` as given: nothing open ends the minimize, else no change. */
function endMinimizeIfEmpty(entries: Record<string, ApprovalEntry>): { minimized?: false } {
  return Object.keys(entries).length === 0 ? { minimized: false } : {}
}

export const useApprovalStore = create<ApprovalStoreState>()((set, get) => ({
  entries: {},
  queued: {},
  decidedHere: {},
  closedIds: {},
  hostEpoch: {},
  minimized: false,

  setMinimized: (v) => set((s) => ({ minimized: v && Object.keys(s.entries).length > 0 })),

  applySnapshot: (hostId, approvals) => {
    const open = approvals.filter((a) => a.state === 'open')
    const keep = new Set(open.map((a) => approvalKey(hostId, a.id)))
    const next: Record<string, ApprovalEntry> = {}
    const vanished: string[] = []
    for (const [key, entry] of Object.entries(get().entries)) {
      if (entry.hostId !== hostId) {
        next[key] = entry
      } else if (!keep.has(key)) {
        vanished.push(entry.approval.id)
      }
    }
    for (const a of open) {
      const key = approvalKey(hostId, a.id)
      // Keep the held object when the same request is still open: the dialog is keyed on it.
      next[key] = get().entries[key] ?? { hostId, approval: a }
    }
    // The daemon's snapshot is authoritative: whatever it lists is open now, tombstones or not.
    set({ entries: next, closedIds: without(get().closedIds, hostId), ...endMinimizeIfEmpty(next) })
    return vanished
  },

  applyOpened: (hostId, approval) => {
    if (approval.state !== 'open') return false
    const key = approvalKey(hostId, approval.id)
    const { entries, closedIds } = get()
    if (Object.hasOwn(entries, key)) return false
    // Its `closed` already came through this socket: the daemon closed it, this `opened` is late.
    if (closedIds[hostId]?.includes(approval.id)) return false
    set((s) => ({ entries: { ...s.entries, [key]: { hostId, approval } } }))
    return true
  },

  applyClosed: (hostId, approval) => {
    const key = approvalKey(hostId, approval.id)
    const { entries, queued, decidedHere, closedIds } = get()
    const had = Object.hasOwn(entries, key)
    const ours = Object.hasOwn(decidedHere, key)
    const rest = without(entries, key)
    set({
      entries: rest,
      queued: without(queued, key),
      decidedHere: without(decidedHere, key),
      closedIds: tombstone(closedIds, hostId, approval.id),
      ...endMinimizeIfEmpty(rest),
    })
    if (!had) return 'absent'
    return ours ? 'ours' : 'elsewhere'
  },

  markDecidedHere: (hostId, id) => set((s) => ({ decidedHere: { ...s.decidedHere, [approvalKey(hostId, id)]: true } })),
  unmarkDecidedHere: (hostId, id) => set((s) => ({ decidedHere: without(s.decidedHere, approvalKey(hostId, id)) })),

  queueDecision: (hostId, approval, decision, grant, pauseSession) =>
    set((s) => ({ queued: { ...s.queued, [approvalKey(hostId, approval.id)]: { hostId, approval, decision, grant, ...(pauseSession ? { pauseSession } : {}) } } })),

  takeQueued: (hostId) => {
    const taken: QueuedDecision[] = []
    const rest: Record<string, QueuedDecision> = {}
    for (const [key, q] of Object.entries(get().queued)) {
      if (q.hostId === hostId) taken.push(q)
      else rest[key] = q
    }
    if (taken.length > 0) set({ queued: rest })
    return taken
  },

  forgetHost: (hostId) => set((s) => {
    // Always: a decision may be in flight with nothing yet held for it (before `markDecidedHere`).
    const hostEpoch = { ...s.hostEpoch, [hostId]: (s.hostEpoch[hostId] ?? 0) + 1 }
    const entries = Object.fromEntries(Object.entries(s.entries).filter(([, e]) => e.hostId !== hostId))
    const queued = Object.fromEntries(Object.entries(s.queued).filter(([, q]) => q.hostId !== hostId))
    // `decidedHere` is keyed by the NUL-joined key only: a host id can hold any printable separator but never NUL,
    // so the prefix is exact.
    const prefix = `${hostId}\u0000`
    const decidedHere = Object.fromEntries(Object.entries(s.decidedHere).filter(([key]) => !key.startsWith(prefix)))
    const held = Object.keys(s.entries).length + Object.keys(s.queued).length + Object.keys(s.decidedHere).length
    const left = Object.keys(entries).length + Object.keys(queued).length + Object.keys(decidedHere).length
    if (held === left && !Object.hasOwn(s.closedIds, hostId)) return { hostEpoch }
    return { hostEpoch, entries, queued, decidedHere, closedIds: without(s.closedIds, hostId), ...endMinimizeIfEmpty(entries) }
  }),

  reset: () => set({ entries: {}, queued: {}, decidedHere: {}, closedIds: {}, minimized: false }),
}))

/** The request the dialog shows: the oldest `created_at` across hosts (spec §6.3 "oldest first"); ties by id, then host. */
export const selectCurrent = (s: ApprovalStoreState): ApprovalEntry | null => {
  let best: ApprovalEntry | null = null
  for (const e of Object.values(s.entries)) {
    if (best === null) {
      best = e
      continue
    }
    const a = e.approval
    const b = best.approval
    if (a.created_at < b.created_at || (a.created_at === b.created_at && (a.id < b.id || (a.id === b.id && e.hostId < best.hostId)))) best = e
  }
  return best
}

export const selectOpenCount = (s: ApprovalStoreState): number => Object.keys(s.entries).length

/** The deadline the pill counts down to (U22 (b)): the smallest `deadline_at` across hosts; null when nothing is open. */
export const selectNearestDeadline = (s: ApprovalStoreState): number | null => {
  let nearest: number | null = null
  for (const e of Object.values(s.entries)) if (nearest === null || e.approval.deadline_at < nearest) nearest = e.approval.deadline_at
  return nearest
}

export const selectOpenCountFor = (hostId: string) => (s: ApprovalStoreState): number => {
  let n = 0
  for (const e of Object.values(s.entries)) if (e.hostId === hostId) n++
  return n
}
