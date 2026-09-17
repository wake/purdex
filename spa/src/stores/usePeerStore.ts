// spa/src/stores/usePeerStore.ts — per-host cache of GET /api/peers
// (peer-info-panel spec §3.1).
//
// The endpoint is a fleet inventory, not a per-session lookup: it costs ~2 s
// per call because the daemon resolves the owning agent of every tmux session
// under a 2 s budget. So this store caches per host and refreshes only when
// something asks it to — never on a tab switch, never on a timer. The policy
// that decides *when* lives in `usePeerInfo`, not here and not in a component.
//
// Not persisted and not synced: a peer address describes processes that are
// alive right now, and a stale one sends a message to the wrong agent.
import { create } from 'zustand'
import { fetchPeers, type PeerRecordWire } from '../lib/host-api'

/** The agent owning a row, reduced to what is displayed. */
export interface PeerAgent {
  type: string
  peerName: string
  status: string
}

/** One session's peer row, reduced to what is displayed (spec §3.1). */
export interface PeerRow {
  /** The full address, e.g. `mini-lab/ai-chat4:ai-chat4-ai-chat-story-3a`. */
  address: string
  /**
   * The sessionId-derived address head (`_3k9f2mq4`), `''` when the row has no
   * cc agent.
   *
   * This is the discriminator, not `labelSource`. `labelSource === ''` has two
   * causes — a live conversation that has not named itself (the common one) and
   * a row with no cc agent — and only `ref`/`agent` tells them apart.
   */
  ref: string
  /** The name the conversation calls itself, `''` until it sets one. Display
   *  only: it addresses nothing, and it is never the value copied. */
  label: string
  labelSource: string   // user | ''
  deliverable: boolean
  reason: string        // '' | no_agent | not_cc | inbox_dead | proxy | ambiguous
  /**
   * The tmux server generation this row describes (`<server pid>:<start time>`),
   * `''` when the daemon could not say.
   *
   * Not displayed — it is what makes the join sound. A session code is tmux's
   * `$N` re-encoded and tmux hands `$N` out from zero again after a restart, so
   * `rows[code]` alone can be a *different* session's address. Readers match
   * this against the pane's own generation and treat `''` on either side as
   * unknown, never as a match.
   */
  tmuxInstance: string
  agent: PeerAgent | null
}

/**
 * The envelope's three partial causes, kept because the UI names them: a row
 * missing from a partial envelope is "could not be determined", not "no peer".
 */
export interface PeerEnvelopeFlags {
  partial: boolean
  labelsUnavailable: boolean
  unknownRegistryFiles: string[]
}

export interface PeerHostEntry {
  /** sessionCode → row. Nested by host, never keyed by a composite: a hostId
   *  can itself contain a colon (`mini-lab:278cbm`), so any later split is a bug. */
  rows: Record<string, PeerRow>
  /** When the rows last arrived; 0 means never. Drives the staleness dimming. */
  fetchedAt: number
  envelope: PeerEnvelopeFlags
  error: string | null
  loading: boolean
}

const COMPLETE_ENVELOPE: PeerEnvelopeFlags = { partial: false, labelsUnavailable: false, unknownRegistryFiles: [] }

export function emptyPeerHostEntry(): PeerHostEntry {
  return { rows: {}, fetchedAt: 0, envelope: COMPLETE_ENVELOPE, error: null, loading: false }
}

/** Stable fallback for selectors — a fresh object per call would loop useSyncExternalStore. */
export const EMPTY_PEER_HOST_ENTRY: PeerHostEntry = Object.freeze(emptyPeerHostEntry()) as PeerHostEntry

/**
 * Index the envelope's rows by session code.
 *
 * The rule is positive and has two halves: `row_kind === 'session'` **and** a
 * non-empty `session_code`. Entry rows are excluded by their kind, not by
 * "entry rows happen to carry an empty code" — that is true of one constructor
 * today, not a promise anyone made. `inbox_dead` and `ambiguous` rows are
 * session rows and keep their codes, so they are indexed; what they render is
 * the caller's business.
 */
export function indexPeerRows(peers: PeerRecordWire[]): Record<string, PeerRow> {
  const rows: Record<string, PeerRow> = {}
  for (const p of peers) {
    if (p.row_kind !== 'session') continue
    if (p.session_code === '') continue
    rows[p.session_code] = {
      address: p.address,
      ref: p.ref ?? '',
      label: p.label,
      labelSource: p.label_source,
      deliverable: p.deliverable,
      reason: p.reason,
      tmuxInstance: p.tmux_instance ?? '',
      agent: p.agent
        ? { type: p.agent.type, peerName: p.agent.peer_name ?? '', status: p.agent.status ?? '' }
        : null,
    }
  }
  return rows
}

interface PeerState {
  byHost: Record<string, PeerHostEntry>
  /** Fetch one host's inventory. Deduped while in flight; never throws. */
  refresh: (hostId: string) => Promise<void>
  /** Drop a host's cache and abandon anything in flight for it. */
  forgetHost: (hostId: string) => void
}

/** In-flight request per host, so a second `refresh` joins rather than doubles. */
const inflight = new Map<string, { promise: Promise<void>; abort: AbortController }>()
/**
 * Bumped by `forgetHost`. A cached address belongs to a daemon identity, so an
 * answer that lands after the host was forgotten (removed, re-pointed, token
 * rotated) describes a machine this cache no longer speaks to: dropping the
 * entry is only half the job if the reply can put it straight back.
 */
const generations = new Map<string, number>()

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

export const usePeerStore = create<PeerState>()((set, get) => {
  const patch = (hostId: string, update: Partial<PeerHostEntry>) =>
    set((s) => ({ byHost: { ...s.byHost, [hostId]: { ...(s.byHost[hostId] ?? emptyPeerHostEntry()), ...update } } }))

  return {
    byHost: {},

    refresh: (hostId) => {
      const running = inflight.get(hostId)
      if (running) return running.promise

      const gen = generations.get(hostId) ?? 0
      const abort = new AbortController()
      /** True while this request's answer still belongs to the cache it was sent for. */
      const stillCurrent = () => (generations.get(hostId) ?? 0) === gen

      const run = (async () => {
        // The previous rows stay visible while the (slow) call runs: a refresh
        // must never flash the status bar empty.
        patch(hostId, { loading: true })
        try {
          const env = await fetchPeers(hostId, abort.signal)
          if (!stillCurrent()) return
          // A 200 carrying `ok: false` is a failure the daemon chose to
          // describe rather than a 500 — an inventory it could not build at
          // all (tmux unreachable, registry unreadable). Its `peers` is empty
          // for want of an answer, not because there are none, so indexing it
          // would turn "we could not look" into a confident "no peer" and
          // clear the rows we still have. Treated exactly like a thrown
          // request: keep the rows, keep `fetchedAt`, record the reason.
          if (!env.ok) {
            patch(hostId, { error: env.error || 'peers unavailable', loading: false })
            return
          }
          patch(hostId, {
            rows: indexPeerRows(env.peers),
            fetchedAt: Date.now(),
            envelope: {
              partial: env.partial,
              labelsUnavailable: env.labels_unavailable,
              unknownRegistryFiles: env.unknown_registry_files ?? [],
            },
            error: null,
            loading: false,
          })
        } catch (err) {
          // A failed refresh keeps the rows and their `fetchedAt`: the display
          // goes stale, which the UI already shows, rather than going blank.
          if (!stillCurrent()) return
          patch(hostId, { error: errorText(err), loading: false })
        } finally {
          if (inflight.get(hostId)?.abort === abort) inflight.delete(hostId)
        }
      })()
      inflight.set(hostId, { promise: run, abort })
      return run
    },

    forgetHost: (hostId) => {
      generations.set(hostId, (generations.get(hostId) ?? 0) + 1)
      const running = inflight.get(hostId)
      if (running) {
        inflight.delete(hostId)
        running.abort.abort()
      }
      if (!(hostId in get().byHost)) return
      set((s) => {
        const rest = { ...s.byHost }
        delete rest[hostId]
        return { byHost: rest }
      })
    },
  }
})
