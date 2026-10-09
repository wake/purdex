// spa/src/stores/useTeamUiStore.ts — how the person arranged each team on THIS device (team interface spec R5, R8, R17,
// §4.6, §4.7): member order, collapsed, panel mode, and the workspace a ghost lead row lives in. Device-local (purdex-team-ui),
// never part of profile sync: a team is a host's, the arrangement is the device's.
//
// Pruning has exactly two triggers (plan review #5): a roster frame from a connected host (`forgetTeams`: the teams that
// ended while the person was away go with the next frame, snapshot after a reconnect included) and the host's deletion
// (`forgetHostTeams`). A disconnect, an endpoint re-point (`forgetHost` elsewhere) or the WS hook's unmount prune nothing:
// the arrangement outlives the connection.
//
// The bead setting (spec P7, §4.9 "device-local") lives here too: `useUISettingsStore` is projected into Profile Sync, so a
// field there would be synced and need a settings-ordinal bump.
//
// `memberOrder` changes identity only through `setMemberOrder`, so `useTeamViews(memberOrder)` recomputes on a reorder
// and not when the person collapses a group or switches the panel mode.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'

export type PanelMode = 'full' | 'line'

interface Slices {
  /** Per team key, the member session ids in the order the person arranged (the lead is never in it). */
  memberOrder: Record<string, string[]>
  collapsed: Record<string, boolean>
  /** Absent = `full` (R17). */
  panelMode: Record<string, PanelMode>
  ghostWorkspace: Record<string, string>
}

interface TeamUiState extends Slices {
  /** Show the host icon next to each member bead (spec P7); default on. */
  teamBeadHost: boolean
  setTeamBeadHost: (v: boolean) => void
  setMemberOrder: (teamKey: string, order: readonly string[]) => void
  setCollapsed: (teamKey: string, collapsed: boolean) => void
  setPanelMode: (teamKey: string, mode: PanelMode) => void
  setGhostWorkspace: (teamKey: string, workspaceId: string | null) => void
  forgetTeams: (hostId: string, liveTeamKeys: readonly string[]) => void
  forgetHostTeams: (hostId: string) => void
  snapshotHostTeams: (hostId: string) => Slices
  restoreHostTeams: (snapshot: Slices) => void
}

const EMPTY: Slices = { memberOrder: {}, collapsed: {}, panelMode: {}, ghostWorkspace: {} }

/** A team key is `<hostId>\0<teamId>` (team-views `teamKeyOf`); a host's keys start with `<hostId>\0`. */
const hostPrefix = (hostId: string) => `${hostId}\u0000`

/** `record` without the keys `drop` says to remove — the same object when none goes. */
function without<T>(record: Record<string, T>, drop: (key: string) => boolean): Record<string, T> {
  let out: Record<string, T> | null = null
  for (const key of Object.keys(record)) {
    if (!drop(key)) continue
    out ??= { ...record }
    delete out[key]
  }
  return out ?? record
}

function pick<T>(record: Record<string, T>, keep: (key: string) => boolean): Record<string, T> {
  const out: Record<string, T> = {}
  for (const key of Object.keys(record)) if (keep(key)) out[key] = record[key]
  return out
}

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const DANGEROUS = new Set(['__proto__', 'prototype', 'constructor'])

/** Persisted data is untrusted: keep only well-formed entries of each slice. */
function heal(persisted: unknown): Slices {
  const p = isRecord(persisted) ? persisted : {}
  const entries = (v: unknown) => (isRecord(v) ? Object.entries(v).filter(([k]) => !DANGEROUS.has(k)) : [])
  return {
    memberOrder: Object.fromEntries(entries(p.memberOrder).filter(([, v]) => Array.isArray(v) && v.every((x) => typeof x === 'string'))) as Slices['memberOrder'],
    collapsed: Object.fromEntries(entries(p.collapsed).filter(([, v]) => v === true)) as Slices['collapsed'],
    panelMode: Object.fromEntries(entries(p.panelMode).filter(([, v]) => v === 'line')) as Slices['panelMode'],
    ghostWorkspace: Object.fromEntries(entries(p.ghostWorkspace).filter(([, v]) => typeof v === 'string' && v !== '')) as Slices['ghostWorkspace'],
  }
}

export const useTeamUiStore = create<TeamUiState>()(
  persist(
    (set, get) => ({
      ...EMPTY,
      teamBeadHost: true,
      setTeamBeadHost: (v) => set((s) => (s.teamBeadHost === v ? s : { teamBeadHost: v })),
      setMemberOrder: (teamKey, order) => set((s) => {
        const current = s.memberOrder[teamKey]
        if (current && current.length === order.length && current.every((id, i) => id === order[i])) return s
        return { memberOrder: { ...s.memberOrder, [teamKey]: [...order] } }
      }),
      setCollapsed: (teamKey, collapsed) => set((s) => {
        if (collapsed === (s.collapsed[teamKey] === true)) return s
        return { collapsed: collapsed ? { ...s.collapsed, [teamKey]: true } : without(s.collapsed, (k) => k === teamKey) }
      }),
      setPanelMode: (teamKey, mode) => set((s) => {
        if (mode === (s.panelMode[teamKey] ?? 'full')) return s
        return { panelMode: mode === 'line' ? { ...s.panelMode, [teamKey]: 'line' } : without(s.panelMode, (k) => k === teamKey) }
      }),
      setGhostWorkspace: (teamKey, workspaceId) => set((s) => {
        if (workspaceId === null || workspaceId === '') {
          return teamKey in s.ghostWorkspace ? { ghostWorkspace: without(s.ghostWorkspace, (k) => k === teamKey) } : s
        }
        if (s.ghostWorkspace[teamKey] === workspaceId) return s
        return { ghostWorkspace: { ...s.ghostWorkspace, [teamKey]: workspaceId } }
      }),
      forgetTeams: (hostId, liveTeamKeys) => set((s) => {
        const prefix = hostPrefix(hostId)
        const live = new Set(liveTeamKeys)
        const stale = (k: string) => k.startsWith(prefix) && !live.has(k)
        const next = {
          memberOrder: without(s.memberOrder, stale), collapsed: without(s.collapsed, stale),
          panelMode: without(s.panelMode, stale), ghostWorkspace: without(s.ghostWorkspace, stale),
        }
        const same = next.memberOrder === s.memberOrder && next.collapsed === s.collapsed
          && next.panelMode === s.panelMode && next.ghostWorkspace === s.ghostWorkspace
        return same ? s : next
      }),
      forgetHostTeams: (hostId) => get().forgetTeams(hostId, []),
      snapshotHostTeams: (hostId) => {
        const s = get()
        const prefix = hostPrefix(hostId)
        const mine = (k: string) => k.startsWith(prefix)
        return {
          memberOrder: pick(s.memberOrder, mine), collapsed: pick(s.collapsed, mine),
          panelMode: pick(s.panelMode, mine), ghostWorkspace: pick(s.ghostWorkspace, mine),
        }
      },
      restoreHostTeams: (snapshot) => set((s) => ({
        memberOrder: { ...s.memberOrder, ...snapshot.memberOrder },
        collapsed: { ...s.collapsed, ...snapshot.collapsed },
        panelMode: { ...s.panelMode, ...snapshot.panelMode },
        ghostWorkspace: { ...s.ghostWorkspace, ...snapshot.ghostWorkspace },
      })),
    }),
    {
      name: STORAGE_KEYS.TEAM_UI,
      storage: purdexStorage,
      partialize: (s) => ({ memberOrder: s.memberOrder, collapsed: s.collapsed, panelMode: s.panelMode, ghostWorkspace: s.ghostWorkspace, teamBeadHost: s.teamBeadHost }),
      merge: (persisted, current) => ({
        ...current, ...heal(persisted),
        teamBeadHost: isRecord(persisted) && typeof persisted.teamBeadHost === 'boolean' ? persisted.teamBeadHost : true,
      }),
    },
  ),
)
