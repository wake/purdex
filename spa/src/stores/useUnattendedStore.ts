// spa/src/stores/useUnattendedStore.ts — per host, whether its daemon supports 無人值守模式 and what its switch says
// (unattended spec D-U23-5, D-U23-6; plan PU-2a). Fed only by the daemon: the `team.unattended` WS branch
// (unattended-ws.ts) writes the state, the `/api/info` probe (unattended-support.ts) writes the support. Never
// persisted, never synced: each renderer has its own sockets and its own copy, and every new connection replays the
// state as a snapshot, so every window follows a change made anywhere (D-U23-6, D-U23-7).
//
// `support`: 'unknown' until the probe answered (or a frame proved it), 'yes' when the daemon lists
// `relay.unattended.v1`, 'no' when it does not (too old: excluded from the write, D-U23-5).
import { create } from 'zustand'
import type { UnattendedState } from '../lib/team/types'

export type UnattendedSupport = 'unknown' | 'yes' | 'no'

export interface UnattendedHostEntry {
  support: UnattendedSupport
  /** Whether the daemon lists `team.relay_quota.v1` (relay quota, plan RQ-A): absent until the probe answered. */
  quotaSupport?: UnattendedSupport
  /** Whether the daemon lists `team.max_members.v1` (the team cap stepper): absent until the probe answered. */
  maxMembersSupport?: UnattendedSupport
  /** Whether the daemon lists `team.edit.v1` (name / label / colour edit): absent until the probe answered. */
  editSupport?: UnattendedSupport
  /** Whether the daemon lists `team.pause.v1` (the 被擋下 light and release, UP-5b): absent until the probe answered. */
  pauseSupport?: UnattendedSupport
  /** Whether the daemon lists `relay.unanswered.v1` (a final relay card left open past its deadline; the App reads it itself): absent until the probe answered. */
  unansweredSupport?: UnattendedSupport
  /** Whether the daemon lists `team.person.v1` (under 無人值守模式 it refuses lead / adopt cards itself; an older one approves them): absent until the probe answered. */
  personSupport?: UnattendedSupport
  /** The host lists `conversations.stages.v1`: its daemon reports where each sent message is (the Deck's send queue reads that, not the text). */
  stagesSupport?: UnattendedSupport
  /** The host lists `team.max_members.v2`: the cap may go to 9 (name pool v2 spec §5.1). */
  maxMembersV2Support?: UnattendedSupport
  /** The host lists `notify.group_labels.v1`: it takes the group names the app computes for its push titles (notify title spec §3.2). */
  notifyGroupLabelsSupport?: UnattendedSupport
  /** Absent until the daemon's snapshot arrived. */
  state?: UnattendedState
}

/** The host's capability flags: all of them follow one rule (unknown on disconnect / before a probe / after a failed probe). */
const SUPPORT_FLAGS = ['support', 'quotaSupport', 'maxMembersSupport', 'editSupport', 'pauseSupport', 'unansweredSupport', 'personSupport', 'stagesSupport', 'maxMembersV2Support', 'notifyGroupLabelsSupport'] as const
/** Only the probe answers these; `support` is also proven by a `team.unattended` frame (unattended-ws.ts). */
export const PROBE_ONLY_FLAGS = ['quotaSupport', 'maxMembersSupport', 'editSupport', 'pauseSupport', 'unansweredSupport', 'personSupport', 'stagesSupport', 'maxMembersV2Support', 'notifyGroupLabelsSupport'] as const
export type SupportFlag = (typeof SUPPORT_FLAGS)[number]

interface UnattendedStoreState {
  byHost: Record<string, UnattendedHostEntry>
  setSupport: (hostId: string, support: UnattendedSupport) => void
  setQuotaSupport: (hostId: string, support: UnattendedSupport) => void
  setMaxMembersSupport: (hostId: string, support: UnattendedSupport) => void
  setEditSupport: (hostId: string, support: UnattendedSupport) => void
  setPauseSupport: (hostId: string, support: UnattendedSupport) => void
  setUnansweredSupport: (hostId: string, support: UnattendedSupport) => void
  setPersonSupport: (hostId: string, support: UnattendedSupport) => void
  setStagesSupport: (hostId: string, support: UnattendedSupport) => void
  setMaxMembersV2Support: (hostId: string, support: UnattendedSupport) => void
  setNotifyGroupLabelsSupport: (hostId: string, support: UnattendedSupport) => void
  /** Every capability flag of the host (unattended, relay quota, max members, edit) is unknown again: the connection they were
   *  learned on is gone, and a daemon that came back may have lost one (#2309). A no-op for a flag never learned. */
  invalidateSupport: (hostId: string, flags?: readonly SupportFlag[]) => void
  applyState: (hostId: string, state: UnattendedState) => void
  forgetHost: (hostId: string) => void
  reset: () => void
}

export const useUnattendedStore = create<UnattendedStoreState>()((set) => ({
  byHost: {},
  setSupport: (hostId, support) => set((s) => {
    const cur = s.byHost[hostId]
    if (cur?.support === support) return s
    return { byHost: { ...s.byHost, [hostId]: { ...cur, support } } }
  }),
  setQuotaSupport: (hostId, quotaSupport) => set((s) => {
    const cur = s.byHost[hostId]
    if (cur?.quotaSupport === quotaSupport) return s
    return { byHost: { ...s.byHost, [hostId]: { ...cur, support: cur?.support ?? 'unknown', quotaSupport } } }
  }),
  setMaxMembersSupport: (hostId, maxMembersSupport) => set((s) => {
    const cur = s.byHost[hostId]
    if (cur?.maxMembersSupport === maxMembersSupport) return s
    return { byHost: { ...s.byHost, [hostId]: { ...cur, support: cur?.support ?? 'unknown', maxMembersSupport } } }
  }),
  invalidateSupport: (hostId, flags = SUPPORT_FLAGS) => set((s) => {
    const cur = s.byHost[hostId]
    if (!cur) return s
    const known = (v: UnattendedSupport | undefined) => v !== undefined && v !== 'unknown'
    if (!flags.some((k) => known(cur[k]))) return s
    const next = { ...cur }
    for (const k of flags) if (next[k] !== undefined) next[k] = 'unknown' // the switch's `state` is not a capability: it stays
    return { byHost: { ...s.byHost, [hostId]: next } }
  }),
  setEditSupport: (hostId, editSupport) => set((s) => {
    const cur = s.byHost[hostId]
    if (cur?.editSupport === editSupport) return s
    return { byHost: { ...s.byHost, [hostId]: { ...cur, support: cur?.support ?? 'unknown', editSupport } } }
  }),
  setPauseSupport: (hostId, pauseSupport) => set((s) => {
    const cur = s.byHost[hostId]
    if (cur?.pauseSupport === pauseSupport) return s
    return { byHost: { ...s.byHost, [hostId]: { ...cur, support: cur?.support ?? 'unknown', pauseSupport } } }
  }),
  setUnansweredSupport: (hostId, unansweredSupport) => set((s) => {
    const cur = s.byHost[hostId]
    if (cur?.unansweredSupport === unansweredSupport) return s
    return { byHost: { ...s.byHost, [hostId]: { ...cur, support: cur?.support ?? 'unknown', unansweredSupport } } }
  }),
  setMaxMembersV2Support: (hostId, maxMembersV2Support) => set((s) => {
    const cur = s.byHost[hostId]
    if (cur?.maxMembersV2Support === maxMembersV2Support) return s
    return { byHost: { ...s.byHost, [hostId]: { ...cur, support: cur?.support ?? 'unknown', maxMembersV2Support } } }
  }),
  setNotifyGroupLabelsSupport: (hostId, notifyGroupLabelsSupport) => set((s) => {
    const cur = s.byHost[hostId]
    if (cur?.notifyGroupLabelsSupport === notifyGroupLabelsSupport) return s
    return { byHost: { ...s.byHost, [hostId]: { ...cur, support: cur?.support ?? 'unknown', notifyGroupLabelsSupport } } }
  }),
  setStagesSupport: (hostId, stagesSupport) => set((s) => {
    const cur = s.byHost[hostId]
    if (cur?.stagesSupport === stagesSupport) return s
    return { byHost: { ...s.byHost, [hostId]: { ...cur, support: cur?.support ?? 'unknown', stagesSupport } } }
  }),
  setPersonSupport: (hostId, personSupport) => set((s) => {
    const cur = s.byHost[hostId]
    if (cur?.personSupport === personSupport) return s
    return { byHost: { ...s.byHost, [hostId]: { ...cur, support: cur?.support ?? 'unknown', personSupport } } }
  }),
  applyState: (hostId, state) => set((s) => {
    const cur = s.byHost[hostId]
    return { byHost: { ...s.byHost, [hostId]: { ...cur, support: cur?.support ?? 'unknown', state } } }
  }),
  forgetHost: (hostId) => set((s) => {
    if (!Object.hasOwn(s.byHost, hostId)) return s
    const { [hostId]: _gone, ...rest } = s.byHost
    return { byHost: rest }
  }),
  reset: () => set({ byHost: {} }),
}))
