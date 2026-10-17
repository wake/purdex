// spa/src/stores/useJournalMembersStore.ts — the member lines of the team panel (JV-2, journal-v5 spec §3.1 / §4.3): per team
// member, its current task and its last report to the leader, both taken from the LEADER's journal (members run no journal).
// Never persisted: it is a read of the leader's host.
//
// Gate: `support[host]` is the live answer of `journal.members.v1` for the current connection; absent = unknown (disconnected, or
// the probe has not answered) and nothing is read or shown. A read that lands after its host was forgotten (`epoch`) or the
// connection ended (`fence`) is dropped. One read per leader at a time (single-flight). A failed read keeps what is held.
import { create } from 'zustand'
import { getJournalMembers } from '../lib/journal/api'
import type { JournalMember } from '../lib/journal/types'

export const membersKey = (hostId: string, leadSessionId: string): string => `${hostId}\u0000${leadSessionId}`

interface MembersState {
  support: Record<string, boolean>
  byLead: Record<string, JournalMember[]>
  epoch: Record<string, number>
  /** The probe's answer for the current connection. */
  setSupport: (hostId: string, yes: boolean) => void
  /** The connection is gone or a new probe starts: support is unknown again (what is held stays, but is not shown). */
  fence: (hostId: string) => void
  /** A removed or re-pointed host: what the old daemon said is not the new one's. */
  forgetHost: (hostId: string) => void
  /** Read the leader's member lines once (no-op without live support, or while a read of the same leader is out). */
  load: (hostId: string, leadSessionId: string) => Promise<void>
  reset: () => void
}

const flights = new Set<string>()
const drop = <T,>(m: Record<string, T>, hostId: string): Record<string, T> => { const { [hostId]: _g, ...rest } = m; return rest }

export const useJournalMembersStore = create<MembersState>()((set, get) => ({
  support: {},
  byLead: {},
  epoch: {},
  setSupport: (hostId, yes) => set((s) => ({ support: { ...s.support, [hostId]: yes } })),
  fence: (hostId) => set((s) => (hostId in s.support ? { support: drop(s.support, hostId) } : s)),
  forgetHost: (hostId) => {
    const prefix = `${hostId}\u0000`
    for (const k of [...flights]) if (k.startsWith(prefix)) flights.delete(k)
    set((s) => ({
      support: drop(s.support, hostId),
      byLead: Object.fromEntries(Object.entries(s.byLead).filter(([k]) => !k.startsWith(prefix))),
      epoch: { ...s.epoch, [hostId]: (s.epoch[hostId] ?? 0) + 1 },
    }))
  },
  load: async (hostId, leadSessionId) => {
    const key = membersKey(hostId, leadSessionId)
    if (get().support[hostId] !== true || flights.has(key)) return
    const epoch = get().epoch[hostId] ?? 0
    flights.add(key)
    try {
      const res = await getJournalMembers(hostId, leadSessionId)
      const s = get()
      if (!flights.has(key) || (s.epoch[hostId] ?? 0) !== epoch || s.support[hostId] !== true) return
      // 404 (no unit yet) or a member whose lead is elsewhere (409): nothing to show.
      set({ byLead: { ...s.byLead, [key]: res.kind === 'ok' ? res.value : [] } })
    } catch {
      // A failed read keeps what is held; the next trigger (a journal.changed, a remount) asks again.
    } finally {
      flights.delete(key)
    }
  },
  reset: () => { flights.clear(); set({ support: {}, byLead: {}, epoch: {} }) },
}))
