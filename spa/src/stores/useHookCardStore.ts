// spa/src/stores/useHookCardStore.ts — the open hook cards (`hook_ask`, `hook_permission`) this window knows of.
//
// The Mac App draws no card for them (the terminal it shows is the answer surface; approval-ws.ts keeps them out of
// useApprovalStore), but the Dock badge counts every open one (app-badge spec §1, §4.1). So they are held here, fed by
// the same `approval.request` frames: a snapshot replaces the host's whole set, `opened` adds, `closed` removes.
// Never persisted; every new connection replays the open set. `closedIds` are per-host tombstones, as in
// useApprovalStore: a `closed` that arrives before its `opened` must not let the late `opened` revive the card.
import { create } from 'zustand'
import type { Approval } from '../lib/team/types'
import { approvalKey } from './useApprovalStore'

/** Tombstones kept per host; past this the oldest is dropped (same bound as useApprovalStore). */
export const TOMBSTONES_PER_HOST = 256

export interface HookCardEntry {
  hostId: string
  approval: Approval
}

interface HookCardState {
  /** Keyed by `approvalKey(hostId, id)` — the identity every window computes the same way. */
  entries: Record<string, HookCardEntry>
  closedIds: Record<string, string[]>
  applySnapshot: (hostId: string, approvals: Approval[]) => void
  /** False when it was already held, is not open, or was closed before (tombstoned). */
  applyOpened: (hostId: string, approval: Approval) => boolean
  applyClosed: (hostId: string, approval: Approval) => void
  forgetHost: (hostId: string) => void
  reset: () => void
}

function withoutHost(closedIds: Record<string, string[]>, hostId: string): Record<string, string[]> {
  if (!Object.hasOwn(closedIds, hostId)) return closedIds
  const next = { ...closedIds }
  delete next[hostId]
  return next
}

export const useHookCardStore = create<HookCardState>()((set, get) => ({
  entries: {},
  closedIds: {},

  applySnapshot: (hostId, approvals) => set((s) => {
    const next: Record<string, HookCardEntry> = {}
    for (const [key, e] of Object.entries(s.entries)) if (e.hostId !== hostId) next[key] = e
    for (const a of approvals) {
      if (a.state !== 'open') continue
      const key = approvalKey(hostId, a.id)
      next[key] = s.entries[key] ?? { hostId, approval: a }
    }
    // The snapshot is authoritative: whatever it lists is open now, tombstones or not.
    return { entries: next, closedIds: withoutHost(s.closedIds, hostId) }
  }),

  applyOpened: (hostId, approval) => {
    if (approval.state !== 'open') return false
    const key = approvalKey(hostId, approval.id)
    const { entries, closedIds } = get()
    if (Object.hasOwn(entries, key)) return false
    if (closedIds[hostId]?.includes(approval.id)) return false
    set((s) => ({ entries: { ...s.entries, [key]: { hostId, approval } } }))
    return true
  },

  applyClosed: (hostId, approval) => set((s) => {
    const key = approvalKey(hostId, approval.id)
    const entries = { ...s.entries }
    delete entries[key]
    const had = s.closedIds[hostId] ?? []
    if (had.includes(approval.id)) return { entries }
    const kept = had.length >= TOMBSTONES_PER_HOST ? had.slice(had.length - TOMBSTONES_PER_HOST + 1) : had
    return { entries, closedIds: { ...s.closedIds, [hostId]: [...kept, approval.id] } }
  }),

  forgetHost: (hostId) => set((s) => ({
    entries: Object.fromEntries(Object.entries(s.entries).filter(([, e]) => e.hostId !== hostId)),
    closedIds: withoutHost(s.closedIds, hostId),
  })),

  reset: () => set({ entries: {}, closedIds: {} }),
}))
