// spa/src/stores/useJournalMembersStore.ts — the member lines of the team panel (JV-2, journal-v5 spec §3.1 / §4.3): per team
// member, its current task and its last report to the leader, both taken from the LEADER's journal (members run no journal).
// Never persisted: it is a read of the leader's host.
//
// Gate: `support[host]` is the live answer of `journal.members.v1` for the current connection; absent = unknown (disconnected, or
// the probe has not answered) and nothing is read or shown. A read that lands after its host was forgotten (`epoch`) or the
// connection ended (`fence`: each one starts a new generation, even when support is declared again right after) is dropped. One read per leader at a time (single-flight, by a token of its own: a finished read only removes ITS marker); a load asked for meanwhile becomes one re-read when it lands. A failed read keeps what is held.
import { create } from 'zustand'
import { getJournalMembers } from '../lib/journal/api'
import type { JournalMember } from '../lib/journal/types'

export const staleKey = (hostId: string, unitKey: string): string => `${hostId}\u0000${unitKey}`
export const membersKey = (hostId: string, leadSessionId: string): string => `${hostId}\u0000${leadSessionId}`

interface MembersState {
  support: Record<string, boolean>
  byLead: Record<string, JournalMember[]>
  epoch: Record<string, number>
  /** Connection generation per host: bumped by every `fence`. */
  gens: Record<string, number>
  /** Per `<host>\0<unit key>`: how many journal.changed events named that unit. Kept here, not read off the journal store, which holds only the
   *  units the loader loaded (the active tab's): a background team's unit is not there and its events would never be seen. */
  stale: Record<string, number>
  /** The probe's answer for the current connection. */
  setSupport: (hostId: string, yes: boolean) => void
  /** A `journal.changed` named this unit of this host. */
  noteChanged: (hostId: string, unitKey: string) => void
  /** The connection is gone or a new probe starts: support is unknown again (what is held stays, but is not shown). */
  fence: (hostId: string) => void
  /** A removed or re-pointed host: what the old daemon said is not the new one's. */
  forgetHost: (hostId: string) => void
  /** Read the leader's member lines once (no-op without live support, or while a read of the same leader is out). */
  load: (hostId: string, leadSessionId: string) => Promise<void>
  reset: () => void
}

let flightSeq = 0
/** The token of the read now out per leader. */
const flights = new Map<string, number>()
/** Leaders asked again while a read was out. */
const again = new Set<string>()
const drop = <T,>(m: Record<string, T>, hostId: string): Record<string, T> => { const { [hostId]: _g, ...rest } = m; return rest }

export const useJournalMembersStore = create<MembersState>()((set, get) => ({
  support: {},
  byLead: {},
  epoch: {},
  gens: {},
  stale: {},
  setSupport: (hostId, yes) => set((s) => ({ support: { ...s.support, [hostId]: yes } })),
  noteChanged: (hostId, unitKey) => set((s) => {
    const k = staleKey(hostId, unitKey)
    return { stale: { ...s.stale, [k]: (s.stale[k] ?? 0) + 1 } }
  }),
  fence: (hostId) => set((s) => ({ support: drop(s.support, hostId), gens: { ...s.gens, [hostId]: (s.gens[hostId] ?? 0) + 1 } })),
  forgetHost: (hostId) => {
    const prefix = `${hostId}\u0000`
    for (const k of [...flights.keys()]) if (k.startsWith(prefix)) flights.delete(k)
    for (const k of [...again]) if (k.startsWith(prefix)) again.delete(k)
    set((s) => ({
      support: drop(s.support, hostId),
      byLead: Object.fromEntries(Object.entries(s.byLead).filter(([k]) => !k.startsWith(prefix))),
      stale: Object.fromEntries(Object.entries(s.stale).filter(([k]) => !k.startsWith(prefix))),
      epoch: { ...s.epoch, [hostId]: (s.epoch[hostId] ?? 0) + 1 },
    }))
  },
  load: async (hostId, leadSessionId) => {
    const key = membersKey(hostId, leadSessionId)
    if (get().support[hostId] !== true) return
    if (flights.has(key)) { again.add(key); return }
    const epoch = get().epoch[hostId] ?? 0
    const gen = get().gens[hostId] ?? 0
    const token = ++flightSeq
    flights.set(key, token)
    try {
      const res = await getJournalMembers(hostId, leadSessionId)
      const s = get()
      if (flights.get(key) !== token || (s.epoch[hostId] ?? 0) !== epoch || (s.gens[hostId] ?? 0) !== gen || s.support[hostId] !== true) return
      // 404 (no unit yet) or a member whose lead is elsewhere (409): nothing to show.
      set({ byLead: { ...s.byLead, [key]: res.kind === 'ok' ? res.value : [] } })
    } catch {
      // A failed read keeps what is held; the next trigger (a journal.changed, a remount) asks again.
    } finally {
      if (flights.get(key) === token) {
        flights.delete(key)
        if (again.delete(key)) void get().load(hostId, leadSessionId)
      }
    }
  },
  reset: () => { flights.clear(); again.clear(); set({ support: {}, byLead: {}, epoch: {}, gens: {}, stale: {} }) },
}))
