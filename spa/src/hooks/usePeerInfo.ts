// spa/src/hooks/usePeerInfo.ts — the one place that decides *when* peer data is
// fetched (peer-info-panel spec §3.3), and the one both landing sites read.
//
// The policy lives in a hook rather than in the status bar or the panel for a
// reason that is testable: `/api/peers` costs ~2 s, so a fetch on every tab
// switch would put a two-second, tmux-heavy daemon job behind every click. With
// the rule here, "switching tabs issues no fetch" is a test somebody can write;
// spread across two components it is only a hope.
import { useEffect, useState } from 'react'
import { useHostStore } from '../stores/useHostStore'
import { EMPTY_PEER_HOST_ENTRY, usePeerStore, type PeerEnvelopeFlags, type PeerRow } from '../stores/usePeerStore'
import { useSessionCwdStore } from '../stores/useSessionCwdStore'

/**
 * How long a peer address stays trustworthy on screen.
 *
 * Default labels are *place* addresses: renaming a tmux session, or a second
 * agent appearing in one, changes an address with nothing in the SPA hearing
 * about it. Past this age the value is dimmed — it still copies, because a
 * click whose meaning changes with an invisible timer is a mis-click waiting to
 * happen (spec §3.4); refreshing is its own control.
 */
export const PEER_STALE_AFTER_MS = 60_000

/**
 * Does a cached row describe the same tmux server as the pane in front of us?
 *
 * A session code is tmux's `$N` re-encoded, and tmux hands `$N` out from zero
 * again after a restart — so a row cached before a restart can carry a
 * *different* session's address under this pane's code. `''` on either side is
 * "the daemon could not say", and an unknown is never a match: the cost of
 * withholding a real address is a dash on screen, the cost of showing a wrong
 * one is a message delivered to a stranger's agent.
 */
function sameGeneration(pane: string | null, row: string): boolean {
  return !!pane && pane !== '' && row !== '' && pane === row
}

export interface PeerInfo {
  /**
   * This session's peer row — `null` when the host's answer has none for it,
   * and equally when the row it has describes another tmux generation.
   */
  row: PeerRow | null
  /** `pane_current_path` for this session; `''` when not read yet. */
  cwd: string
  /** The host's last peers answer: its three partial causes. */
  envelope: PeerEnvelopeFlags
  /** The host's peer rows have been asked for at least once. */
  fetched: boolean
  /** When the rows arrived; 0 = never. */
  fetchedAt: number
  /** Rows at least `PEER_STALE_AFTER_MS` old. Never true before the first answer. */
  stale: boolean
  /** Either store has a request in flight. Previous values stay on screen. */
  loading: boolean
  /** The peers error, or `null`. */
  error: string | null
  /** The cwd error, or `null` — separate, because one can fail without the other. */
  cwdError: string | null
  /** The host is reachable. Nothing is ever fetched when this is false. */
  connected: boolean
  /** Refresh both stores now, however fresh the cache is. */
  refresh: () => void
}

/**
 * Peer data for one pane.
 *
 * The trigger is a predicate, not an event: a peers fetch happens when this
 * hook needs host H, H is `connected`, and H has no entry in the store yet.
 * Nothing about tmux is consulted — `connected` with `tmuxState` still
 * `undefined` is the normal moment just after a WS opens, and `unavailable` is
 * a host whose daemon answers but has no tmux sessions, which is a real answer.
 * The cheap cwd read is the exception: it re-runs per activated session.
 *
 * `tmuxInstance` is the pane's own tmux generation — `Session.tmux_instance`,
 * the value the daemon stamped on the payload that carried this session. Only a
 * row from the same generation is returned; see {@link sameGeneration}. It gates
 * what is *read*, never what is fetched: a pane whose generation is not known
 * yet still warms the host's cache, it just shows nothing until it is.
 */
export function usePeerInfo(hostId: string | null, sessionCode: string | null, tmuxInstance: string | null): PeerInfo {
  const connected = useHostStore((s) => (hostId ? s.runtime[hostId]?.status === 'connected' : false))

  const cached = usePeerStore((s) => (hostId && sessionCode ? s.byHost[hostId]?.rows[sessionCode] ?? null : null))
  const row = cached && sameGeneration(tmuxInstance, cached.tmuxInstance) ? cached : null
  const envelope = usePeerStore((s) => (hostId ? s.byHost[hostId]?.envelope : undefined) ?? EMPTY_PEER_HOST_ENTRY.envelope)
  const fetchedAt = usePeerStore((s) => (hostId ? s.byHost[hostId]?.fetchedAt ?? 0 : 0))
  const peersLoading = usePeerStore((s) => (hostId ? s.byHost[hostId]?.loading ?? false : false))
  const error = usePeerStore((s) => (hostId ? s.byHost[hostId]?.error ?? null : null))
  /** The host has been asked at least once — an entry exists, error or not. */
  const fetched = usePeerStore((s) => (hostId ? s.byHost[hostId] !== undefined : false))

  const cwd = useSessionCwdStore((s) => (hostId && sessionCode ? s.byHost[hostId]?.[sessionCode]?.cwd ?? '' : ''))
  const cwdLoading = useSessionCwdStore((s) => (hostId && sessionCode ? s.byHost[hostId]?.[sessionCode]?.loading ?? false : false))
  const cwdError = useSessionCwdStore((s) => (hostId && sessionCode ? s.byHost[hostId]?.[sessionCode]?.error ?? null : null))

  // The expensive one: once per host, and only while it is reachable. An entry
  // that exists — even one holding an error — counts as fetched, so a failing
  // host is not retried on every render; the refresh control is the way back.
  useEffect(() => {
    if (!hostId || !connected || fetched) return
    void usePeerStore.getState().refresh(hostId)
  }, [hostId, connected, fetched])

  // The cheap one: a single tmux call, re-read for each session that becomes
  // active, because `pane_current_path` follows a `cd` and the old reading is
  // simply wrong once the user has navigated.
  useEffect(() => {
    if (!hostId || !sessionCode || !connected) return
    void useSessionCwdStore.getState().refresh(hostId, sessionCode)
  }, [hostId, sessionCode, connected])

  // Staleness is a function of the clock, so nothing would re-render at the
  // boundary on its own. The clock is read once at mount and then only by a
  // timeout scheduled for the exact moment the current answer goes stale —
  // one timer per answer, not a poll. A newer answer is always ahead of this
  // reading, so it reads fresh until its own timer fires.
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (fetchedAt === 0) return
    const remaining = fetchedAt + PEER_STALE_AFTER_MS - Date.now()
    if (remaining <= 0) return  // already past it; `now` says so without a timer
    const timer = setTimeout(() => setNow(Date.now()), remaining)
    return () => clearTimeout(timer)
  }, [fetchedAt])

  return {
    row,
    cwd,
    envelope,
    fetched,
    fetchedAt,
    // `>=`, so the timer scheduled for exactly this boundary is enough to flip it.
    stale: fetchedAt > 0 && now - fetchedAt >= PEER_STALE_AFTER_MS,
    loading: peersLoading || cwdLoading,
    error,
    cwdError,
    connected,
    refresh: () => {
      if (!hostId || !connected) return
      void usePeerStore.getState().refresh(hostId)
      if (sessionCode) void useSessionCwdStore.getState().refresh(hostId, sessionCode)
    },
  }
}
