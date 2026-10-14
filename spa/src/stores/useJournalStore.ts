// spa/src/stores/useJournalStore.ts — the workbook journal, per host and unit (J-2a). Never persisted, never synced.
//
// Support has two layers. `support[host]` is the live answer of the current connection: absent = unknown (the host is
// disconnected or its probe has not answered). `sticky[host]` is the LAST yes/no answer ever received and survives a
// disconnect (`fence` only clears `support`), so a journal host never flashes back to the workbook view while it
// reconnects; a host that never answered has no sticky answer and uses the old UI. Only `forgetHost` (re-point / removal)
// clears it: what the old daemon said is not the new one's.
import { create } from 'zustand'
import { forgetViewMemosOfHost } from '../lib/journal/view-memory'

interface JournalState {
  /** Live support of the current connection; absent = unknown. */
  support: Record<string, boolean>
  /** The last yes/no answer, kept through unknown; absent = never answered. */
  sticky: Record<string, boolean>
  /** Connection generation per host: bumped by `fence`. */
  gens: Record<string, number>

  /** The connection is gone or a new probe starts: support is unknown, the sticky answer stays. */
  fence: (hostId: string) => void
  /** The probe's answer for the current generation. */
  setSupport: (hostId: string, yes: boolean) => void
  forgetHost: (hostId: string) => void
  reset: () => void
}

const drop = <T,>(m: Record<string, T>, hostId: string): Record<string, T> => { const { [hostId]: _g, ...rest } = m; return rest }

export const useJournalStore = create<JournalState>()((set) => ({
  support: {}, sticky: {}, gens: {},
  fence: (hostId) => set((s) => ({ support: drop(s.support, hostId), gens: { ...s.gens, [hostId]: (s.gens[hostId] ?? 0) + 1 } })),
  setSupport: (hostId, yes) => set((s) => ({ support: { ...s.support, [hostId]: yes }, sticky: { ...s.sticky, [hostId]: yes } })),
  forgetHost: (hostId) => {
    forgetViewMemosOfHost(hostId)
    set((s) => ({ support: drop(s.support, hostId), sticky: drop(s.sticky, hostId), gens: drop(s.gens, hostId) }))
  },
  reset: () => set({ support: {}, sticky: {}, gens: {} }),
}))

/** The host's last known journal.v1 answer: true / false, or undefined when it never answered (use the old UI). */
export const stickySupport = (hostId: string, state: Pick<JournalState, 'sticky'> = useJournalStore.getState()): boolean | undefined => state.sticky[hostId]
/** The live answer of this connection: true / false, or undefined while unknown. */
export const liveSupport = (hostId: string, state: Pick<JournalState, 'support'> = useJournalStore.getState()): boolean | undefined => state.support[hostId]
