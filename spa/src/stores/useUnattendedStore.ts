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
  /** Absent until the daemon's snapshot arrived. */
  state?: UnattendedState
}

/** The host's capability flags: all of them follow one rule (unknown on disconnect / before a probe / after a failed probe). */
const SUPPORT_FLAGS = ['support', 'quotaSupport', 'maxMembersSupport', 'editSupport'] as const

interface UnattendedStoreState {
  byHost: Record<string, UnattendedHostEntry>
  setSupport: (hostId: string, support: UnattendedSupport) => void
  setQuotaSupport: (hostId: string, support: UnattendedSupport) => void
  setMaxMembersSupport: (hostId: string, support: UnattendedSupport) => void
  setEditSupport: (hostId: string, support: UnattendedSupport) => void
  /** Every capability flag of the host (unattended, relay quota, max members, edit) is unknown again: the connection they were
   *  learned on is gone, and a daemon that came back may have lost one (#2309). A no-op for a flag never learned. */
  invalidateSupport: (hostId: string) => void
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
  invalidateSupport: (hostId) => set((s) => {
    const cur = s.byHost[hostId]
    if (!cur) return s
    const known = (v: UnattendedSupport | undefined) => v !== undefined && v !== 'unknown'
    if (!SUPPORT_FLAGS.some((k) => known(cur[k]))) return s
    const next = { ...cur }
    for (const k of SUPPORT_FLAGS) if (next[k] !== undefined) next[k] = 'unknown' // the switch's `state` is not a capability: it stays
    return { byHost: { ...s.byHost, [hostId]: next } }
  }),
  setEditSupport: (hostId, editSupport) => set((s) => {
    const cur = s.byHost[hostId]
    if (cur?.editSupport === editSupport) return s
    return { byHost: { ...s.byHost, [hostId]: { ...cur, support: cur?.support ?? 'unknown', editSupport } } }
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
