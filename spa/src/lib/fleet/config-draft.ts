// spa/src/lib/fleet/config-draft.ts — what the Hosts page's fleet editors hold while the reader types, kept OUTSIDE the
// components (CLAUDE.md tab-hosted checklist; same idea as lib/nex/worker-draft-memory.ts). The Hosts page is a tab: a tab the
// alive pool does not keep unmounts when the reader switches away, and component state would start empty on the way back.
// Memory only: a reload starts empty. `reset()` is for tests and for the config store's own reset.
import { create } from 'zustand'
import type { DroppedNotice, ProbeBase, ProbeEdit, ProbeEntry } from './config-store'

/** `base`: what each daemon held when the edit began; a save is checked against it (see `ProbeBase`). */
export interface ProbeSession { initial: ProbeEntry; draft: ProbeEntry; isNew: boolean; base: ProbeBase }
/** A fleet-row edit, based on `rev` (the row's revision when the edit began); a newer revision makes it stale. */
export interface FleetRowDraft { order: string; peer: string; enabled: boolean; rev: number }

/** What the probe-host section says about the request in flight: kept here so a tab switch mid-request cannot allow a second save. */
export interface ProbeUi { busy: boolean; dialogError: string | null; listError: string | null; changed: string[] | null }

/** The probe host whose own page the Hosts page shows (F-2d). `isNew`: it is not in any daemon's list yet (its draft starts blank). */
export interface ProbeSelection { id: string; isNew: boolean }

interface DraftState {
  selectedProbe: ProbeSelection | null
  /** Picking a probe host, or none, drops the pool host page (one page shows at a time). */
  selectProbe: (sel: ProbeSelection | null) => void
  /** The resource pool host whose page the Hosts page shows (7a-3b; a fleet host id). Kept here so a tab switch keeps it. */
  selectedPool: string | null
  selectPool: (id: string | null) => void
  ui: ProbeUi
  setUi: (patch: Partial<ProbeUi>) => void
  probe: ProbeSession | null
  deleting: string | null
  probeConfirm: { edit: ProbeEdit; notices: DroppedNotice[]; base?: ProbeBase } | null
  fleetRows: Record<string, FleetRowDraft>
  fleetConfirm: Record<string, DroppedNotice[]>
  beginProbe: (entry: ProbeEntry, isNew: boolean, base: ProbeBase) => void
  setProbeDraft: (fn: (d: ProbeEntry) => ProbeEntry) => void
  endProbe: () => void
  setDeleting: (id: string | null) => void
  setProbeConfirm: (c: DraftState['probeConfirm']) => void
  setFleetRow: (hostId: string, draft: FleetRowDraft | null) => void
  setFleetConfirm: (hostId: string, notices: DroppedNotice[] | null) => void
  reset: () => void
}

const emptyUi: ProbeUi = { busy: false, dialogError: null, listError: null, changed: null }
const empty = { selectedProbe: null, selectedPool: null, ui: emptyUi, probe: null, deleting: null, probeConfirm: null, fleetRows: {}, fleetConfirm: {} }

export const useFleetDraftStore = create<DraftState>((set) => ({
  ...empty,
  selectProbe: (selectedProbe) => set({ selectedProbe, selectedPool: null }),
  selectPool: (selectedPool) => set((s) => ({ selectedPool, selectedProbe: selectedPool ? null : s.selectedProbe })),
  setUi: (patch) => set((s) => ({ ui: { ...s.ui, ...patch } })),
  beginProbe: (entry, isNew, base) => set({ probe: { initial: entry, draft: entry, isNew, base } }),
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
