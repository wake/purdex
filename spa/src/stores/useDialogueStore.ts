// spa/src/stores/useDialogueStore.ts — the session dialogue log's data layer (plan T14a): per (host, session), the newest-first
// recaps answer and the turn summaries seen so far. Never persisted, never synced: every window reloads what it shows.
//
// Rules:
//   * A host that does not list `dialogue.v1` (`setSupport`, filled by unattended-support.ts's /api/info probe) gets no
//     request from any loader; nothing is cached for it.
//   * `regenerate` → `queued` marks the session "generating" until a recap newer than the newest one cached at request time
//     is loaded. A `dialogue.recap` event (only for a session the store already holds) keeps/sets the mark until the re-read shows the recap it
//     announced; the re-read is retried a few times, and giving up clears the mark and sets `loadFailed`.
//   * A request that lands after its host was forgotten, or after support went away (disconnect, probe), is dropped (`epoch`).
import { create } from 'zustand'
import { fetchRecaps, fetchTurnSummaries, requestRecap } from '../lib/dialogue/api'
import type { Recap, RecapsResponse, RecapState, RecapTrigger, TurnRange, TurnSummary } from '../lib/dialogue/types'

export interface DialogueEntry {
  /** Last recaps answer (newest first); null before the first one. */
  recaps: RecapsResponse | null
  loading: boolean
  /** Turn summaries seen so far, by turn number. Read ranges with `summariesInRange`. */
  summaries: Record<number, TurnSummary>
  /** A recap was queued and has not shown up yet. */
  generating: boolean
  /** The newest recap id cached when the request was queued (0: none): a recap with a larger id ends `generating`. */
  generatingBase: number
  /** The request number of the recaps answer whose counts (`can_generate`, `newest_turn_no`, `last_recap_to_no`) are cached. */
  recapsSeq: number
  /** The last recap read failed (a `dialogue.recap` event's re-read gave up); cleared by the next good read. */
  loadFailed: boolean
}

export type RegenerateOutcome = RecapState | 'unsupported' | 'error'

interface DialogueState {
  support: Record<string, boolean>
  byHost: Record<string, Record<string, DialogueEntry>>
  /** Bumped by `forgetHost` and `setSupport(false)`: tells a request still out that its host was forgotten or its connection went away. */
  epoch: Record<string, number>

  setSupport: (hostId: string, supported: boolean) => void
  /** The newest `limit` recaps (the daemon defaults to 20). */
  loadRecaps: (hostId: string, sessionId: string, limit?: number) => Promise<void>
  /** Turn summaries in a range (oldest first); [] on failure or without support. */
  loadTurnSummaries: (hostId: string, sessionId: string, range: TurnRange) => Promise<TurnSummary[]>
  /** The newest `limit` turn summaries. */
  loadLatestSummaries: (hostId: string, sessionId: string, limit: number) => Promise<TurnSummary[]>
  /** The turns after turn number `afterNo` (exclusive: asks `from = afterNo + 1`), e.g. after the newest recap's `to_no`. */
  loadSummariesAfter: (hostId: string, sessionId: string, afterNo: number) => Promise<TurnSummary[]>
  /** Ask for a recap. `queued` marks the session generating; a failed request answers 'error'. */
  regenerate: (hostId: string, sessionId: string, trigger: RecapTrigger) => Promise<RegenerateOutcome>
  /** A `dialogue.recap` host event's value. */
  applyRecapEvent: (hostId: string, value: string) => void
  forgetHost: (hostId: string) => void
  reset: () => void
}

const emptyEntry = (): DialogueEntry => ({ recaps: null, loading: false, summaries: {}, generating: false, generatingBase: 0, recapsSeq: 0, loadFailed: false })

export const selectDialogueSupport = (hostId: string, state: Pick<DialogueState, 'support'> = useDialogueStore.getState()): boolean =>
  state.support[hostId] === true

export const selectDialogueEntry = (state: Pick<DialogueState, 'byHost'>, hostId: string, sessionId: string): DialogueEntry | undefined =>
  state.byHost[hostId]?.[sessionId]

/** The cached turn summaries with `from <= turn_no <= to`, oldest first. */
export function summariesInRange(entry: DialogueEntry | undefined, from: number, to: number): TurnSummary[] {
  if (!entry) return []
  return Object.values(entry.summaries).filter((t) => t.turn_no >= from && t.turn_no <= to).sort((a, b) => a.turn_no - b.turn_no)
}

function withEntry(s: DialogueState, hostId: string, sessionId: string, fn: (e: DialogueEntry) => DialogueEntry): Pick<DialogueState, 'byHost'> {
  const host = s.byHost[hostId] ?? {}
  return { byHost: { ...s.byHost, [hostId]: { ...host, [sessionId]: fn(host[sessionId] ?? emptyEntry()) } } }
}

/** Recap rows by id, newest first. A recap never changes or goes away once written, so answers can be merged in any order. */
function mergeRecaps(old: readonly Recap[], fresh: readonly Recap[]): Recap[] {
  const byId = new Map<number, Recap>()
  for (const r of old) byId.set(r.id, r)
  for (const r of fresh) byId.set(r.id, r)
  return [...byId.values()].sort((a, b) => b.id - a.id)
}

const RETRY_DELAYS_MS = [1000, 2000, 4000]

/** Per (host, session), the number of the last recaps GET sent: answers are ordered by when they were sent, not by when they landed. */
const recapsIssued = new Map<string, number>()
/** Per (host, session), the number of the last `dialogue.recap` event: only its re-read may retry or give up (it waits for the newest recap announced). */
const announced = new Map<string, number>()

export const useDialogueStore = create<DialogueState>()((set, get) => {
  // A request still out is the current one only while its host kept the same connection: `setSupport(false)` and `forgetHost` move the epoch.
  const alive = (hostId: string, epoch: number) => (get().epoch[hostId] ?? 0) === epoch && get().support[hostId] === true

  async function loadSummaries(hostId: string, sessionId: string, range: TurnRange): Promise<TurnSummary[]> {
    if (!get().support[hostId]) return []
    const epoch = get().epoch[hostId] ?? 0
    let page: TurnSummary[]
    try {
      page = await fetchTurnSummaries(hostId, sessionId, range)
    } catch {
      return []
    }
    if (!alive(hostId, epoch)) return []
    set((s) => withEntry(s, hostId, sessionId, (e) => ({ ...e, summaries: { ...e.summaries, ...Object.fromEntries(page.map((t) => [t.turn_no, t])) } })))
    return page
  }

  /** 'ok': landed; 'failed': the request failed; 'dropped': no support, or its connection went away. */
  async function readRecaps(hostId: string, sessionId: string, limit?: number): Promise<'ok' | 'failed' | 'dropped'> {
    if (!get().support[hostId]) return 'dropped'
    const epoch = get().epoch[hostId] ?? 0
    const key = `${hostId}\u0000${sessionId}`
    const seq = (recapsIssued.get(key) ?? 0) + 1
    recapsIssued.set(key, seq)
    set((s) => withEntry(s, hostId, sessionId, (e) => ({ ...e, loading: true })))
    let answer: RecapsResponse
    try {
      answer = await fetchRecaps(hostId, sessionId, limit)
    } catch {
      if (!alive(hostId, epoch)) return 'dropped'
      set((s) => withEntry(s, hostId, sessionId, (e) => ({ ...e, loading: false })))
      return 'failed'
    }
    if (!alive(hostId, epoch)) return 'dropped'
    set((s) => withEntry(s, hostId, sessionId, (e) => {
      const rows = mergeRecaps(e.recaps?.recaps ?? [], answer.recaps)
      // The counts belong to the request sent last; an older one answered late only adds its rows.
      const latest = seq >= e.recapsSeq
      const next: RecapsResponse = latest ? { ...answer, recaps: rows } : { ...(e.recaps as RecapsResponse), recaps: rows }
      return { ...e, recaps: next, recapsSeq: latest ? seq : e.recapsSeq, loading: false, loadFailed: false, generating: e.generating && !((rows[0]?.id ?? 0) > e.generatingBase) }
    }))
    return 'ok'
  }

  async function loadRecaps(hostId: string, sessionId: string, limit?: number): Promise<void> {
    await readRecaps(hostId, sessionId, limit)
  }

  /** A `dialogue.recap` event's re-read: retried (1s, 2s, 4s) while it fails or still shows nothing as new as the announced recap. A newer event's
   *  re-read takes over; giving up (failed reads, or reads that never showed the announced recap) clears the mark and flags the read as failed. */
  async function rereadAnnounced(hostId: string, sessionId: string): Promise<void> {
    const epoch = get().epoch[hostId] ?? 0
    const key = `${hostId}\u0000${sessionId}`
    const mine = (announced.get(key) ?? 0) + 1
    announced.set(key, mine)
    const current = () => alive(hostId, epoch) && announced.get(key) === mine
    for (let attempt = 0; ; attempt++) {
      const outcome = await readRecaps(hostId, sessionId)
      if (outcome === 'dropped' || !current()) return
      const e = selectDialogueEntry(get(), hostId, sessionId)
      if (outcome === 'ok' && !e?.generating) return
      if (attempt >= RETRY_DELAYS_MS.length) {
        set((s) => withEntry(s, hostId, sessionId, (x) => ({ ...x, generating: false, loadFailed: true })))
        return
      }
      await new Promise((r) => setTimeout(r, RETRY_DELAYS_MS[attempt]))
      if (!current()) return
    }
  }

  return {
    support: {}, byHost: {}, epoch: {},

    // Support going away (disconnect, probe start, probe failed, capability gone) ends the connection the requests out were made on: the epoch moves
    // so their answers are dropped, and nothing waits on a job queued over it (a later read shows what it made).
    setSupport: (hostId, supported) => set((s) => {
      if (supported) return s.support[hostId] === true ? s : { support: { ...s.support, [hostId]: true } }
      const host = s.byHost[hostId]
      const byHost = host
        ? { ...s.byHost, [hostId]: Object.fromEntries(Object.entries(host).map(([sid, e]) => [sid, e.generating || e.loading ? { ...e, generating: false, loading: false } : e])) }
        : s.byHost
      return { support: { ...s.support, [hostId]: false }, byHost, epoch: { ...s.epoch, [hostId]: (s.epoch[hostId] ?? 0) + 1 } }
    }),

    loadRecaps,
    loadTurnSummaries: (hostId, sessionId, range) => loadSummaries(hostId, sessionId, range),
    loadLatestSummaries: (hostId, sessionId, limit) => loadSummaries(hostId, sessionId, { limit }),
    loadSummariesAfter: (hostId, sessionId, afterNo) => loadSummaries(hostId, sessionId, { from: afterNo + 1 }),

    regenerate: async (hostId, sessionId, trigger) => {
      if (!get().support[hostId]) return 'unsupported'
      const epoch = get().epoch[hostId] ?? 0
      let state: RecapState
      try {
        state = await requestRecap(hostId, sessionId, trigger)
      } catch {
        return 'error'
      }
      if (alive(hostId, epoch) && state === 'queued') {
        set((s) => withEntry(s, hostId, sessionId, (e) => ({ ...e, generating: true, generatingBase: e.recaps?.recaps[0]?.id ?? 0 })))
      }
      return state
    },

    applyRecapEvent: (hostId, value) => {
      if (!get().support[hostId]) return
      let sessionId: unknown
      let recapId: unknown
      try {
        const v: unknown = JSON.parse(value)
        const o = typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : {}
        sessionId = o.session_id
        recapId = o.recap_id
      } catch {
        return
      }
      if (typeof sessionId !== 'string' || sessionId === '' || !get().byHost[hostId]?.[sessionId]) return
      // Generating stays until the re-read shows the announced recap (a failed read must not hand the button back for finished work).
      set((s) => withEntry(s, hostId, sessionId as string, (e) => {
        const base = typeof recapId === 'number' && Number.isFinite(recapId) ? recapId - 1 : (e.recaps?.recaps[0]?.id ?? 0)
        return { ...e, generating: true, generatingBase: e.generating ? Math.max(e.generatingBase, base) : base }
      }))
      void rereadAnnounced(hostId, sessionId)
    },

    forgetHost: (hostId) => set((s) => {
      const { [hostId]: _h, ...byHost } = s.byHost
      const { [hostId]: _s, ...support } = s.support
      return { byHost, support, epoch: { ...s.epoch, [hostId]: (s.epoch[hostId] ?? 0) + 1 } }
    }),

    reset: () => set({ support: {}, byHost: {}, epoch: {} }),
  }
})
