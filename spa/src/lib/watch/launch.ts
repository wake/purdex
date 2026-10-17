// spa/src/lib/watch/launch.ts — the watcher's launch panel buttons (plan §1 and §11, W5e): 啟動守門員 → `POST /api/watch/start`,
// 叫醒守門員 → `POST /api/watch/wake` with an empty prompt. Both are admin actions of the host's own token (`hostFetch`); the
// daemon's refusal text (`detail`) is what the panel shows.
//
// The state lives here, not in the panel: the panel unmounts when the tab is switched away (CLAUDE.md, tab-hosted checklist), and
// a watcher that is coming up must still read as busy when the tab comes back. There is one slot, because there is one watcher:
//   sending → the request is on its way (busy)
//   busy    → the daemon took it (200 / 202 of a wake, 202 of a start); busy until the watcher registers, i.e. the status store
//             says awake (a `watch.changed` read). Nothing else releases it but a host change: no `watch.changed` = still busy.
//   timeout → not busy: the daemon took the request but no watcher registered within `WATCH_LAUNCH_TIMEOUT_MS`. The button can be
//             pressed again; a registration (awake) or a host change clears the line.
//   notice  → not busy: already awake (start 200), a cooldown (409) or an error. The button can be pressed again.
// The timeout timer lives here too (not in the panel), follows the slot (one subscription below arms it when a slot becomes busy
// and disarms it whenever the slot is anything else), and only ever acts on the request it was armed for (seq).
// A request is applied only while it is still the current one on the host connection it was sent on (seq + `connectionKey`): a
// host re-pointed at another daemon must not receive the old daemon's answer as its own state.
import { create } from 'zustand'
import { useHostStore } from '../../stores/useHostStore'
import { connectionKey } from '../host-connection-key'
import { hostFetch } from '../host-api'
import { deriveGuardian, rereadWatchStatus, useWatchStatusStore, type WatchEntry } from './status-store'

export type LaunchKind = 'start' | 'wake'

export type LaunchSlot =
  | { phase: 'sending'; kind: LaunchKind; hostId: string; seq: number }
  | { phase: 'busy'; kind: LaunchKind; hostId: string; seq: number; startingOn?: { alias: string; daemonId: string } }
  /** Busy for `WATCH_LAUNCH_TIMEOUT_MS` and the watcher never registered: not busy any more, with a line saying so. */
  | { phase: 'timeout'; kind: LaunchKind; hostId: string; seq: number }
  | { phase: 'awake_already'; kind: LaunchKind; hostId: string; seq: number }
  /** `until` is the epoch ms the daemon said (NaN: it did not give a readable time); `at` is when it said so (the "today" of the time shown). */
  | { phase: 'cooldown'; kind: LaunchKind; hostId: string; seq: number; until: number; at: number }
  /** `detail` is the daemon's own text; `network` is a request that never got an answer. */
  | { phase: 'error'; kind: LaunchKind; hostId: string; seq: number; detail?: string; status?: number; network?: boolean }

interface LaunchState {
  slot: LaunchSlot | null
  seq: number
}

export const useWatchLaunchStore = create<LaunchState>(() => ({ slot: null, seq: 0 }))

/** How long a taken request may stay busy while no watcher registers. */
export const WATCH_LAUNCH_TIMEOUT_MS = 120_000

let timeoutTimer: { seq: number; handle: ReturnType<typeof setTimeout> } | null = null
const disarmTimeout = (): void => {
  if (timeoutTimer) clearTimeout(timeoutTimer.handle)
  timeoutTimer = null
}
const armTimeout = (seq: number): void => {
  disarmTimeout()
  const handle = setTimeout(() => {
    timeoutTimer = null
    const s = useWatchLaunchStore.getState().slot
    // Only the request this timer was armed for: a newer request (another seq) is not its to release.
    if (s && s.phase === 'busy' && s.seq === seq) {
      useWatchLaunchStore.setState({ slot: { phase: 'timeout', kind: s.kind, hostId: s.hostId, seq } })
    }
  }, WATCH_LAUNCH_TIMEOUT_MS)
  timeoutTimer = { seq, handle }
}
useWatchLaunchStore.subscribe((state) => {
  const s = state.slot
  if (s?.phase === 'busy') {
    if (timeoutTimer?.seq !== s.seq) armTimeout(s.seq)
  } else {
    disarmTimeout()
  }
})

export const isLaunchBusy =(slot: LaunchSlot | null): boolean => slot?.phase === 'sending' || slot?.phase === 'busy'

const PATHS: Record<LaunchKind, string> = { start: '/api/watch/start', wake: '/api/watch/wake' }

const identityOf = (hostId: string): string | null => {
  const h = useHostStore.getState().hosts[hostId]
  return h ? connectionKey(h) : null
}

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const str = (v: unknown): string | undefined => (typeof v === 'string' && v !== '' ? v : undefined)

const awakeNow = (): boolean => deriveGuardian(useWatchStatusStore.getState().byHost).state === 'awake'

/** Where a start goes when no watcher is registered: any reachable watch host (the daemon picks the host that starts it). */
export function startTarget(byHost: Record<string, WatchEntry>, hostOrder: readonly string[]): string | undefined {
  const ok = (id: string): boolean => byHost[id]?.kind === 'ok'
  const ordered = hostOrder.find(ok)
  return ordered ?? Object.keys(byHost).find(ok)
}

/** Press: ask the daemon of `hostId`. Ignored while a request is on its way or the watcher is coming up. */
export async function requestLaunch(kind: LaunchKind, hostId: string): Promise<void> {
  const before = useWatchLaunchStore.getState()
  if (isLaunchBusy(before.slot)) return
  const identity = identityOf(hostId)
  if (identity === null) return
  const seq = before.seq + 1
  useWatchLaunchStore.setState({ seq, slot: { phase: 'sending', kind, hostId, seq } })
  // Still this request, on the connection it was sent on?
  const current = (): boolean => {
    const s = useWatchLaunchStore.getState().slot
    return s !== null && s.seq === seq && s.hostId === hostId && identityOf(hostId) === identity
  }
  const settle = (slot: LaunchSlot | null): void => { if (current()) useWatchLaunchStore.setState({ slot }) }

  let res: Response
  try {
    res = await hostFetch(hostId, PATHS[kind], {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: kind === 'wake' ? '{}' : undefined,
    })
  } catch {
    return settle({ phase: 'error', kind, hostId, seq, network: true })
  }
  const body: unknown = await res.json().catch(() => null)

  if (res.status === 200 || res.status === 202) {
    // The watcher registered while the request was on its way: nothing to wait for.
    if (awakeNow()) return settle(null)
    if (kind === 'start' && res.status === 200) {
      settle({ phase: 'awake_already', kind, hostId, seq })
      rereadWatchStatus(hostId) // this App's status was stale (it showed no watcher): read it again
      return
    }
    const alias = isObj(body) ? str(body.host) : undefined
    const daemonId = isObj(body) ? str(body.host_id) : undefined
    const startingOn = kind === 'start' && alias !== undefined ? { alias, daemonId: daemonId ?? '' } : undefined
    return settle({ phase: 'busy', kind, hostId, seq, startingOn })
  }
  const code = isObj(body) ? str(body.error) : undefined
  const detail = isObj(body) ? str(body.detail) : undefined
  if (res.status === 409 && code === 'cooldown') {
    return settle({ phase: 'cooldown', kind, hostId, seq, until: detail === undefined ? NaN : Date.parse(detail), at: Date.now() })
  }
  return settle({ phase: 'error', kind, hostId, seq, detail, status: res.status })
}

/**
 * Installed once from main.tsx. Returns how to stop it.
 *  - The watcher registering (the status store says awake, after a `watch.changed` read) ends whatever the slot holds.
 *  - A host that is gone, not connected or re-pointed at another daemon takes its slot with it.
 */
export function startWatchLaunch(): () => void {
  const unsubStatus = useWatchStatusStore.subscribe((next, prev) => {
    if (next.byHost === prev.byHost) return
    if (useWatchLaunchStore.getState().slot && deriveGuardian(next.byHost).state === 'awake') useWatchLaunchStore.setState({ slot: null })
  })
  const unsubHosts = useHostStore.subscribe((next, prev) => {
    const slot = useWatchLaunchStore.getState().slot
    if (!slot) return
    const was = prev.hosts[slot.hostId]
    const now = next.hosts[slot.hostId]
    if (!now || next.runtime[slot.hostId]?.status !== 'connected' || (was && connectionKey(was) !== connectionKey(now))) {
      useWatchLaunchStore.setState({ slot: null })
    }
  })
  return () => { unsubStatus(); unsubHosts() }
}
