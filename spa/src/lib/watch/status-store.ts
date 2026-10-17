// spa/src/lib/watch/status-store.ts — what each connected host's daemon says about the watcher (plan T-a3.1).
//
// The App reads `GET /api/watch/status` of every connected host when it connects, and again whenever that host's daemon
// pushes `watch.changed` (`rereadWatchStatus`, called from the host event socket); there is no timer.
// A reply is written only under the generation it began in: a disconnect, a re-connect or a removal moves it, so an
// answer of the old connection is dropped. Nothing here is persisted.
import { create } from 'zustand'
import { useHostStore } from '../../stores/useHostStore'
import { fetchWatchStatus } from './api'
import type { WatchStatus } from './status-types'

/** The tmux session of the watcher until the status carries its own `tmux_session` (plan §13, A3). */
export const PDX_WATCH_SESSION = 'pdx-watch'

export type WatchEntry =
  | { kind: 'ok'; status: WatchStatus; at: number }
  | { kind: 'unsupported'; at?: number }
  | { kind: 'error'; at?: number }

interface WatchStatusState {
  /** App host id → what that host's daemon said. */
  byHost: Record<string, WatchEntry>
  set: (hostId: string, entry: WatchEntry) => void
  forget: (hostId: string) => void
  reset: () => void
}

export const useWatchStatusStore = create<WatchStatusState>((set) => ({
  byHost: {},
  set: (hostId, entry) => set((s) => ({ byHost: { ...s.byHost, [hostId]: entry } })),
  forget: (hostId) => set((s) => {
    if (!(hostId in s.byHost)) return s
    const { [hostId]: _gone, ...byHost } = s.byHost
    return { byHost }
  }),
  reset: () => set({ byHost: {} }),
}))

export interface Guardian {
  state: 'awake' | 'resting' | 'none' | 'unknown'
  /** The holder's daemon host id. */
  hostId?: string
  /** The App host whose daemon is the holder (undefined when no connected host is it). */
  appHostId?: string
  alias?: string
  sessionId?: string
  since?: string
  mode?: string
  /** The tmux session name of the watcher on the holder. */
  tmuxSession: string
  /** False when the holder is known only from another host's `current`: this App is not connected to it. */
  reachable: boolean
}

/** Milliseconds of an RFC 3339 `since`; a Go zero time or garbage is unknown (null). */
export function sinceMs(since: string | undefined): number | null {
  if (!since) return null
  const ms = Date.parse(since)
  return Number.isNaN(ms) || ms <= 0 ? null : ms
}

interface Held { appHostId: string; status: WatchStatus }

function pickHolder(byHost: Record<string, WatchEntry>, state: 'awake' | 'resting'): Held | null {
  const held: Held[] = []
  for (const [appHostId, e] of Object.entries(byHost)) {
    if (e.kind === 'ok' && e.status.self.state === state) held.push({ appHostId, status: e.status })
  }
  if (held.length === 0) return null
  // awake: the earliest since (an unknown one last); resting: the latest since (an unknown one least); then the smaller host id.
  const key = (h: Held): number => {
    const ms = sinceMs(h.status.self.since)
    return state === 'awake' ? (ms ?? Infinity) : -(ms ?? -Infinity)
  }
  held.sort((a, b) => key(a) - key(b) || (a.status.self.host_id < b.status.self.host_id ? -1 : a.status.self.host_id > b.status.self.host_id ? 1 : 0))
  return held[0]
}

function fromHeld(state: 'awake' | 'resting', h: Held): Guardian {
  const { self, watcher, current } = h.status
  const own = watcher && watcher.host_id === self.host_id ? watcher : undefined
  return {
    state, hostId: self.host_id, appHostId: h.appHostId,
    alias: own?.alias || (current && current.host_id === self.host_id ? current.alias : undefined) || undefined,
    sessionId: self.session_id, since: self.since, mode: h.status.mode || undefined,
    tmuxSession: own?.tmux_session || PDX_WATCH_SESSION, reachable: true,
  }
}

/** Where the watcher is, from every host's answer (load-control spec §12.3). */
export function deriveGuardian(byHost: Record<string, WatchEntry>): Guardian {
  const awake = pickHolder(byHost, 'awake')
  if (awake) return fromHeld('awake', awake)
  const resting = pickHolder(byHost, 'resting')
  if (resting) return fromHeld('resting', resting)
  for (const e of Object.values(byHost)) {
    if (e.kind === 'ok' && e.status.current) {
      const c = e.status.current
      return { state: c.state, hostId: c.host_id, alias: c.alias || undefined, sessionId: c.session_id || undefined, since: c.since, tmuxSession: PDX_WATCH_SESSION, reachable: false }
    }
  }
  const anyOk = Object.values(byHost).some((e) => e.kind === 'ok')
  return { state: anyOk ? 'none' : 'unknown', tmuxSession: PDX_WATCH_SESSION, reachable: true }
}

/** The running reader's re-read of one host (null while `startWatchStatus` is not running). */
let rereader: ((hostId: string) => void) | null = null

/** A `watch.changed` push: read that host's status again, if it is connected. A late answer of an old connection is dropped. */
export function rereadWatchStatus(hostId: string): void {
  rereader?.(hostId)
}

/** Installed once from main.tsx. Returns how to stop it. */
export function startWatchStatus(): () => void {
  const gens = new Map<string, number>()
  const bump = (hostId: string): number => { const g = (gens.get(hostId) ?? 0) + 1; gens.set(hostId, g); return g }
  const isConnected = (hostId: string): boolean => {
    const s = useHostStore.getState()
    return !!s.hosts[hostId] && s.runtime[hostId]?.status === 'connected'
  }

  const read = (hostId: string): void => {
    const gen = gens.get(hostId) ?? bump(hostId)
    void fetchWatchStatus(hostId).then((r) => {
      if (gens.get(hostId) !== gen || !isConnected(hostId)) return
      useWatchStatusStore.getState().set(hostId, r.kind === 'ok' ? { kind: 'ok', status: r.status, at: Date.now() } : { kind: r.kind, at: Date.now() })
    })
  }
  // A push starts a new read under a new generation, so an older answer still in flight cannot land after it.
  rereader = (hostId) => { if (isConnected(hostId)) { bump(hostId); read(hostId) } }

  for (const id of Object.keys(useHostStore.getState().hosts)) if (isConnected(id)) { bump(id); read(id) }

  const unsub = useHostStore.subscribe((next, prev) => {
    for (const id of Object.keys(prev.hosts)) {
      if (!next.hosts[id]) { bump(id); useWatchStatusStore.getState().forget(id) }
    }
    for (const id of Object.keys(next.hosts)) {
      const now = next.runtime[id]?.status === 'connected'
      const was = prev.runtime[id]?.status === 'connected' && !!prev.hosts[id]
      if (now && !was) { bump(id); read(id) }
      else if (!now && was) { bump(id); useWatchStatusStore.getState().forget(id) }
    }
  })

  return () => {
    unsub()
    rereader = null
  }
}
