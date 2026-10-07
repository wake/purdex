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

export const useApprovalStore = create<ApprovalStoreState>()((set, get) => ({
  entries: {},
  queued: {},
  decidedHere: {},
  closedIds: {},

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
    set({ entries: next, closedIds: without(get().closedIds, hostId) })
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
    set({
      entries: without(entries, key),
      queued: without(queued, key),
      decidedHere: without(decidedHere, key),
      closedIds: tombstone(closedIds, hostId, approval.id),
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

  reset: () => set({ entries: {}, queued: {}, decidedHere: {}, closedIds: {} }),
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

export const selectOpenCountFor = (hostId: string) => (s: ApprovalStoreState): number => {
  let n = 0
  for (const e of Object.values(s.entries)) if (e.hostId === hostId) n++
  return n
}
