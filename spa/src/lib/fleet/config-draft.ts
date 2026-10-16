// spa/src/lib/fleet/config-draft.ts — what the Hosts page's fleet editors hold while the reader types, kept OUTSIDE the
// components (CLAUDE.md tab-hosted checklist; same idea as lib/nex/worker-draft-memory.ts). The Hosts page is a tab: a tab the
// alive pool does not keep unmounts when the reader switches away, and component state would start empty on the way back.
// Memory only: a reload starts empty. `reset()` is for tests and for the config store's own reset.
import { create } from 'zustand'
import type { DroppedNotice, ProbeEntry } from './config-store'

export interface ProbeSession { initial: ProbeEntry; draft: ProbeEntry; isNew: boolean }
/** A fleet-row edit, based on `rev` (the row's revision when the edit began); a newer revision makes it stale. */
export interface FleetRowDraft { order: string; peer: string; enabled: boolean; rev: number }

interface DraftState {
  probe: ProbeSession | null
  deleting: string | null
  probeConfirm: { entries: ProbeEntry[]; notices: DroppedNotice[] } | null
  fleetRows: Record<string, FleetRowDraft>
  fleetConfirm: Record<string, DroppedNotice[]>
  beginProbe: (entry: ProbeEntry, isNew: boolean) => void
  setProbeDraft: (fn: (d: ProbeEntry) => ProbeEntry) => void
  endProbe: () => void
  setDeleting: (id: string | null) => void
  setProbeConfirm: (c: DraftState['probeConfirm']) => void
  setFleetRow: (hostId: string, draft: FleetRowDraft | null) => void
  setFleetConfirm: (hostId: string, notices: DroppedNotice[] | null) => void
  reset: () => void
}

const empty = { probe: null, deleting: null, probeConfirm: null, fleetRows: {}, fleetConfirm: {} }

export const useFleetDraftStore = create<DraftState>((set) => ({
  ...empty,
  beginProbe: (entry, isNew) => set({ probe: { initial: entry, draft: entry, isNew } }),
  setProbeDraft: (fn) => set((s) => (s.probe ? { probe: { ...s.probe, draft: fn(s.probe.draft) } } : s)),
  endProbe: () => set({ probe: null }),
  setDeleting: (id) => set({ deleting: id }),
  setProbeConfirm: (probeConfirm) => set({ probeConfirm }),
  setFleetRow: (hostId, draft) => set((s) => {
    const fleetRows = { ...s.fleetRows }
    if (draft) fleetRows[hostId] = draft
    else delete fleetRows[hostId]
    return { fleetRows }
  }),
  setFleetConfirm: (hostId, notices) => set((s) => {
    const fleetConfirm = { ...s.fleetConfirm }
    if (notices) fleetConfirm[hostId] = notices
    else delete fleetConfirm[hostId]
    return { fleetConfirm }
  }),
  reset: () => set({ ...empty }),
}))
