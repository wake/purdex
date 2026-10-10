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
import { panelMinWidth } from '../components/team/panel-layout'
import { useUISettingsStore } from './useUISettingsStore'

/** Where the panel area sits (team spec §4.4 Round 3): in the title bar, or in the pane as one line, the full list, or enlarged. */
export type PanelMode = 'titlebar' | 'line' | 'full' | 'max'
/** The states in the pane: what the area goes back to when it leaves the title bar. */
export type PaneMode = Exclude<PanelMode, 'titlebar'>
const isPaneMode = (v: unknown): v is PaneMode => v === 'line' || v === 'full' || v === 'max'
const isPanelMode = (v: unknown): v is PanelMode => v === 'titlebar' || isPaneMode(v)

interface Slices {
  /** Per team key, the member session ids in the order the person arranged (the lead is never in it). */
  memberOrder: Record<string, string[]>
  collapsed: Record<string, boolean>
  /** Per team key, the four-state value; absent = `full` (R17). */
  panelMode: Record<string, PanelMode>
  /** Per team key, the pane state the area left when it went to the title bar; absent = `full`. */
  panelLast: Record<string, PaneMode>
  ghostWorkspace: Record<string, string>
  /** Per team key, the seat whose workbook the panel area shows in place of the team view (WA-2b writes it). */
  teamDrill: Record<string, DrillSeat>
  /** Per team key, the seats that left the team's roster on a frame from a connected host: newest first, at most ENDED_SEATS_MAX (WA-2b-1). */
  endedSeats: Record<string, EndedSeat[]>
}

export interface DrillSeat { hostId: string; sessionId: string }
/** A seat that left its team; `hostId` is the App host its session lived on, `title` what the panel called it. */
export interface EndedSeat extends DrillSeat { title: string; endedAt: number }
export const ENDED_SEATS_MAX = 20

/** The panel area: its width in px, one value for the whole area (enlarging is the per-team `max` state). */
export interface PanelArea {
  width: number
  /**
   * True while the width is the automatic one (the person never dragged it): it then follows the minimum when the light style
   * or host box moves it. A drag (`setPanelWidth`) clears it, so a width the person chose is kept even when it happens to equal
   * another style's minimum. Absent (an old save, a test fixture) = infer once from `width === the current minimum`.
   */
  followsMin?: boolean
}

export const PANEL_MAX_WIDTH = 720

/**
 * The least width at which a lead + 3 members fit one header row under the CURRENT light style and host box
 * (panel-layout `panelMinWidth`; badge 356, iconDot 412). It is also the default width.
 */
export const currentPanelMin = (): number => {
  const s = useUISettingsStore.getState()
  return panelMinWidth(s.tabIndicatorStyle, s.hostBadgeSidebarBox)
}

const clampWidth = (w: number): number => Math.max(currentPanelMin(), Math.min(PANEL_MAX_WIDTH, Math.round(w)))

interface TeamUiState extends Slices {
  panel: PanelArea
  /** Tabs whose 「工作簿」 toggle is on (WA-2b writes it). */
  workbookTabs: Record<string, true>
  setPanelWidth: (width: number) => void
  /** The four-state value of tabs that belong to no team (WA-2b-1 reads it); starts in the title bar. */
  sharedPanelMode: PanelMode
  sharedPanelLast: PaneMode
  setSharedPanelMode: (mode: PanelMode) => void
  /** An old store held `panel.expanded: true`: the team showing when the area first draws becomes `max` (TeamPanelArea). Not persisted. */
  legacyMax: boolean
  takeLegacyMax: (teamKey: string | null) => void
  setTeamDrill: (teamKey: string, seat: DrillSeat | null) => void
  /** Add seats that just left the team (first = newest); a seat already listed is not recorded again. Cap ENDED_SEATS_MAX. */
  recordEndedSeats: (teamKey: string, seats: readonly EndedSeat[]) => void
  /** A listed seat is on the roster again: it is no longer "ended". */
  clearEndedSeat: (teamKey: string, hostId: string, sessionId: string) => void
  setWorkbookTab: (tabId: string, on: boolean) => void
  /** Show the host icon next to each member bead (spec P7); default on. */
  teamBeadHost: boolean
  setTeamBeadHost: (v: boolean) => void
  /** Trial (TI-6, spec §4.2): which box-shadow the group tabs wear; removed once the user picks. Default `v2`. */
  setMemberOrder: (teamKey: string, order: readonly string[]) => void
  setCollapsed: (teamKey: string, collapsed: boolean) => void
  /** Move the area; a pane state is also remembered as the one to come back to from the title bar. */
  setPanelMode: (teamKey: string, mode: PanelMode) => void
  /** Title bar <-> the pane state last left (the title-bar button, the strip's name / +N). */
  toggleTitleBar: (teamKey: string) => void
  setGhostWorkspace: (teamKey: string, workspaceId: string | null) => void
  forgetTeams: (hostId: string, liveTeamKeys: readonly string[]) => void
  forgetHostTeams: (hostId: string) => void
  snapshotHostTeams: (hostId: string) => Slices
  restoreHostTeams: (snapshot: Slices) => void
}

const EMPTY: Slices = { memberOrder: {}, collapsed: {}, panelMode: {}, panelLast: {}, ghostWorkspace: {}, teamDrill: {}, endedSeats: {} }
const defaultPanel = (): PanelArea => ({ width: currentPanelMin(), followsMin: true })

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

function healPanel(v: unknown): PanelArea {
  if (!isRecord(v)) return defaultPanel()
  const width = typeof v.width === 'number' && Number.isFinite(v.width) ? clampWidth(v.width) : currentPanelMin()
  // No flag in an old save: infer it once (a width at the minimum was never widened).
  return { width, followsMin: typeof v.followsMin === 'boolean' ? v.followsMin : width === currentPanelMin() }
}

/** The host of a `<hostId>\0<teamId>` key, or null when the key is not that shape (either part empty / extra separator). */
function keyHost(key: string): string | null {
  const parts = key.split('\0')
  return parts.length === 2 && parts[0] !== '' && parts[1] !== '' ? parts[0] : null
}

const sameSeat = (a: DrillSeat, b: DrillSeat) => a.hostId === b.hostId && a.sessionId === b.sessionId

/** `seats` in order, each seat once (the first wins), at most ENDED_SEATS_MAX. */
function capEnded(seats: readonly EndedSeat[]): EndedSeat[] {
  const out: EndedSeat[] = []
  for (const e of seats) {
    if (out.length >= ENDED_SEATS_MAX) break
    if (!out.some((o) => sameSeat(o, e))) out.push(e)
  }
  return out
}

function healEnded(v: unknown): EndedSeat[] {
  if (!Array.isArray(v)) return []
  return capEnded(v.filter((e): e is EndedSeat => isRecord(e) && typeof e.hostId === 'string' && e.hostId !== ''
    && typeof e.sessionId === 'string' && e.sessionId !== '' && typeof e.title === 'string'
    && typeof e.endedAt === 'number' && Number.isFinite(e.endedAt))
    .map((e) => ({ hostId: e.hostId, sessionId: e.sessionId, title: e.title, endedAt: e.endedAt })))
}

/** Persisted data is untrusted: keep only well-formed entries of each slice. */
function heal(persisted: unknown): Slices & { panel: PanelArea; workbookTabs: Record<string, true>; sharedPanelMode: PanelMode; sharedPanelLast: PaneMode; legacyMax: boolean } {
  const p = isRecord(persisted) ? persisted : {}
  const entries = (v: unknown) => (isRecord(v) ? Object.entries(v).filter(([k]) => !DANGEROUS.has(k)) : [])
  return {
    memberOrder: Object.fromEntries(entries(p.memberOrder).filter(([, v]) => Array.isArray(v) && v.every((x) => typeof x === 'string'))) as Slices['memberOrder'],
    collapsed: Object.fromEntries(entries(p.collapsed).filter(([, v]) => v === true)) as Slices['collapsed'],
    // An old store kept only `line` here (`full` was the absence); `full` stays the absence in the four-state value too.
    panelMode: Object.fromEntries(entries(p.panelMode).filter(([, v]) => isPanelMode(v) && v !== 'full')) as Slices['panelMode'],
    panelLast: Object.fromEntries(entries(p.panelLast).filter(([, v]) => isPaneMode(v) && v !== 'full')) as Slices['panelLast'],
    sharedPanelMode: isPanelMode(p.sharedPanelMode) ? p.sharedPanelMode : 'titlebar',
    sharedPanelLast: isPaneMode(p.sharedPanelLast) ? p.sharedPanelLast : 'full',
    legacyMax: isRecord(p.panel) && p.panel.expanded === true,
    ghostWorkspace: Object.fromEntries(entries(p.ghostWorkspace).filter(([, v]) => typeof v === 'string' && v !== '')) as Slices['ghostWorkspace'],
    teamDrill: Object.fromEntries(entries(p.teamDrill).filter(([key, v]) => isRecord(v) && typeof v.hostId === 'string' && v.hostId !== ''
      && keyHost(key) !== null && typeof v.sessionId === 'string' && v.sessionId !== '').map(([k, v]) => [k, { hostId: (v as DrillSeat).hostId, sessionId: (v as DrillSeat).sessionId }])),
    endedSeats: Object.fromEntries(entries(p.endedSeats).filter(([key]) => keyHost(key) !== null)
      .map(([key, v]) => [key, healEnded(v)] as const).filter(([, v]) => v.length > 0)),
    workbookTabs: Object.fromEntries(entries(p.workbookTabs).filter(([, v]) => v === true)) as Record<string, true>,
    panel: healPanel(p.panel),
  }
}

export const useTeamUiStore = create<TeamUiState>()(
  persist(
    (set, get) => ({
      ...EMPTY,
      panel: defaultPanel(),
      workbookTabs: {},
      setPanelWidth: (width) => set((s) => {
        if (!Number.isFinite(width)) return s
        const next = clampWidth(width)
        // This is the person's drag: the width is theirs from now on (the follow in the settings subscription uses setState).
        return next === s.panel.width && s.panel.followsMin === false ? s : { panel: { width: next, followsMin: false } }
      }),
      sharedPanelMode: 'titlebar',
      sharedPanelLast: 'full',
      setSharedPanelMode: (mode) => set((s) => {
        const last = mode === 'titlebar' ? s.sharedPanelLast : mode
        return mode === s.sharedPanelMode && last === s.sharedPanelLast ? s : { sharedPanelMode: mode, sharedPanelLast: last }
      }),
      legacyMax: false,
      takeLegacyMax: (teamKey) => {
        if (!get().legacyMax) return
        set({ legacyMax: false })
        if (teamKey !== null) get().setPanelMode(teamKey, 'max')
      },
      setTeamDrill: (teamKey, seat) => set((s) => {
        if (seat === null) return teamKey in s.teamDrill ? { teamDrill: without(s.teamDrill, (k) => k === teamKey) } : s
        const cur = s.teamDrill[teamKey]
        if (cur && cur.hostId === seat.hostId && cur.sessionId === seat.sessionId) return s
        return { teamDrill: { ...s.teamDrill, [teamKey]: { hostId: seat.hostId, sessionId: seat.sessionId } } }
      }),
      recordEndedSeats: (teamKey, seats) => set((s) => {
        const cur = s.endedSeats[teamKey] ?? []
        const fresh = seats.filter((e) => !cur.some((o) => sameSeat(o, e)))
        if (fresh.length === 0) return s
        return { endedSeats: { ...s.endedSeats, [teamKey]: capEnded([...fresh, ...cur]) } }
      }),
      clearEndedSeat: (teamKey, hostId, sessionId) => set((s) => {
        const cur = s.endedSeats[teamKey]
        if (!cur?.some((o) => sameSeat(o, { hostId, sessionId }))) return s
        const next = cur.filter((o) => !sameSeat(o, { hostId, sessionId }))
        return { endedSeats: next.length > 0 ? { ...s.endedSeats, [teamKey]: next } : without(s.endedSeats, (k) => k === teamKey) }
      }),
      setWorkbookTab: (tabId, on) => set((s) => {
        if (on === (s.workbookTabs[tabId] === true)) return s
        return { workbookTabs: on ? { ...s.workbookTabs, [tabId]: true } : without(s.workbookTabs, (k) => k === tabId) }
      }),
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
        const lastNow = s.panelLast[teamKey] ?? 'full'
        const last = mode === 'titlebar' ? lastNow : mode
        if (mode === (s.panelMode[teamKey] ?? 'full') && last === lastNow) return s
        // `full` is the absence in both records.
        return {
          panelMode: mode === 'full' ? without(s.panelMode, (k) => k === teamKey) : { ...s.panelMode, [teamKey]: mode },
          panelLast: last === 'full' ? without(s.panelLast, (k) => k === teamKey) : { ...s.panelLast, [teamKey]: last },
        }
      }),
      toggleTitleBar: (teamKey) => {
        const s = get()
        s.setPanelMode(teamKey, (s.panelMode[teamKey] ?? 'full') === 'titlebar' ? s.panelLast[teamKey] ?? 'full' : 'titlebar')
      },
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
          panelMode: without(s.panelMode, stale), panelLast: without(s.panelLast, stale), ghostWorkspace: without(s.ghostWorkspace, stale),
          teamDrill: without(s.teamDrill, stale), endedSeats: without(s.endedSeats, stale),
        }
        const same = next.memberOrder === s.memberOrder && next.collapsed === s.collapsed
          && next.panelMode === s.panelMode && next.panelLast === s.panelLast && next.ghostWorkspace === s.ghostWorkspace && next.teamDrill === s.teamDrill && next.endedSeats === s.endedSeats
        return same ? s : next
      }),
      forgetHostTeams: (hostId) => get().forgetTeams(hostId, []),
      snapshotHostTeams: (hostId) => {
        const s = get()
        const prefix = hostPrefix(hostId)
        const mine = (k: string) => k.startsWith(prefix)
        return {
          memberOrder: pick(s.memberOrder, mine), collapsed: pick(s.collapsed, mine),
          panelMode: pick(s.panelMode, mine), panelLast: pick(s.panelLast, mine), ghostWorkspace: pick(s.ghostWorkspace, mine),
          teamDrill: pick(s.teamDrill, mine), endedSeats: pick(s.endedSeats, mine),
        }
      },
      restoreHostTeams: (snapshot) => set((s) => ({
        memberOrder: { ...s.memberOrder, ...snapshot.memberOrder },
        collapsed: { ...s.collapsed, ...snapshot.collapsed },
        panelMode: { ...s.panelMode, ...snapshot.panelMode },
        panelLast: { ...s.panelLast, ...snapshot.panelLast },
        ghostWorkspace: { ...s.ghostWorkspace, ...snapshot.ghostWorkspace },
        teamDrill: { ...s.teamDrill, ...snapshot.teamDrill },
        endedSeats: { ...s.endedSeats, ...snapshot.endedSeats },
      })),
    }),
    {
      name: STORAGE_KEYS.TEAM_UI,
      storage: purdexStorage,
      partialize: (s) => ({ memberOrder: s.memberOrder, collapsed: s.collapsed, panelMode: s.panelMode, panelLast: s.panelLast, sharedPanelMode: s.sharedPanelMode, sharedPanelLast: s.sharedPanelLast, ghostWorkspace: s.ghostWorkspace, teamDrill: s.teamDrill, endedSeats: s.endedSeats, panel: s.panel, workbookTabs: s.workbookTabs, teamBeadHost: s.teamBeadHost }),
      merge: (persisted, current) => ({
        ...current, ...heal(persisted),
        teamBeadHost: isRecord(persisted) && typeof persisted.teamBeadHost === 'boolean' ? persisted.teamBeadHost : true,
      }),
    },
  ),
)

// The minimum moves with the light style and the host box (user 2026-10-10, round 5). When it does: a width below the new
// minimum is pulled up to it, and a width the person never dragged (`panel.followsMin`) follows it either way; a width the
// person dragged is kept, even when it equals another style's minimum.
let lastMin = currentPanelMin()
useUISettingsStore.subscribe(() => {
  const min = currentPanelMin()
  if (min === lastMin) return
  const prev = lastMin
  lastMin = min
  const { width, followsMin } = useTeamUiStore.getState().panel
  const follows = followsMin ?? width === prev
  if (width < min || follows) useTeamUiStore.setState({ panel: { width: min, followsMin: follows } })
})
