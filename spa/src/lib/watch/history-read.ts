// spa/src/lib/watch/history-read.ts — reads the 歷史 body's gate records (watcher area spec §6.1): once when it opens (and when the class
// changes), then every 30 s while it is shown and the window is visible. No push. Each read carries a generation and the connection
// it was sent on (as status-store.ts / events-store.ts do): a re-point, a disconnect, a removal, a class change, a newer read or the
// body leaving all move it, so an answer that arrives after that is dropped.
import { useEffect, useState } from 'react'
import { useHostStore } from '../../stores/useHostStore'
import { connectionKey } from '../host-connection-key'
import { fetchGateDecisions } from './api'
import type { GateDecision } from './decisions-types'
import { HISTORY_WINDOW_MS } from './history-view'
import type { HistoryClass } from './view-memory'

export const HISTORY_REREAD_MS = 30_000

export type HistoryState =
  | { kind: 'loading' }
  | { kind: 'ok'; records: GateDecision[]; skipped: number }
  | { kind: 'error'; reason: 'forbidden' | 'server' | 'network' }

const identityOf = (hostId: string): string | null => {
  const h = useHostStore.getState().hosts[hostId]
  return h ? connectionKey(h) : null
}
const isConnected = (hostId: string): boolean => {
  const s = useHostStore.getState()
  return !!s.hosts[hostId] && s.runtime[hostId]?.status === 'connected'
}

/** `active` false (the body is not shown) reads nothing and stops the timer. */
export function useGateHistory(hostId: string | undefined, cls: HistoryClass, active: boolean): HistoryState {
  // The connection identity is part of the key: a re-point restarts the reader against the new endpoint and the old answer is not shown.
  const identity = useHostStore((s) => (hostId && s.hosts[hostId] ? connectionKey(s.hosts[hostId]) : null))
  const connected = useHostStore((s) => !!hostId && s.runtime[hostId]?.status === 'connected')
  // The state is stamped with what it answers, so a class or host change never shows the old answer under the new key.
  const key = `${hostId ?? ''}|${cls}|${identity ?? ''}`
  const [got, setGot] = useState<{ key: string; state: HistoryState }>({ key, state: { kind: 'loading' } })

  useEffect(() => {
    if (!active || !hostId || !connected) return
    let gen = 0
    let stopped = false
    const read = (): void => {
      const mine = ++gen
      const sentOn = identityOf(hostId)
      void fetchGateDecisions(hostId, cls, new Date(Date.now() - HISTORY_WINDOW_MS).toISOString()).then((r) => {
        if (stopped || mine !== gen || !isConnected(hostId) || identityOf(hostId) !== sentOn) return
        setGot({ key, state: r.kind === 'ok' ? { kind: 'ok', records: r.records, skipped: r.skipped } : { kind: 'error', reason: r.kind } })
      })
    }
    const visible = (): boolean => typeof document === 'undefined' || document.visibilityState !== 'hidden'
    if (visible()) read()
    const timer = setInterval(() => { if (visible()) read() }, HISTORY_REREAD_MS)
    // Coming back to the window re-reads at once instead of showing a 30 s-old (or older) list.
    const onVisible = (): void => { if (visible()) read() }
    document.addEventListener('visibilitychange', onVisible)
    return () => { stopped = true; gen++; clearInterval(timer); document.removeEventListener('visibilitychange', onVisible) }
  }, [active, hostId, cls, key, connected])

  return got.key === key ? got.state : { kind: 'loading' }
}
