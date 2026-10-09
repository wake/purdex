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

interface UnattendedStoreState {
  byHost: Record<string, UnattendedHostEntry>
  setSupport: (hostId: string, support: UnattendedSupport) => void
  setQuotaSupport: (hostId: string, support: UnattendedSupport) => void
  setMaxMembersSupport: (hostId: string, support: UnattendedSupport) => void
  setEditSupport: (hostId: string, support: UnattendedSupport) => void
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
