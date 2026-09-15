// spa/src/hooks/useExecutionLease.ts — the control lease THIS pane holds on
// a Nexen execution (spec §4.3.2). Lazy: nothing is acquired until the user
// sends or interrupts. Renewed at ttl/3 while the user is active; after
// 2×ttl of silence the lease is allowed to lapse so another client (phone,
// other tab) can take over without this one actively holding it; the next
// send re-acquires. Released exactly once on teardown. The store's `lease`
// field is written ONLY from attach(control)/renew responses (I11).
import { useCallback, useEffect, useRef } from 'react'
import { attachControl, releaseLease, renewLease } from '../lib/nex/nex-api'
import { getLeaseTtlSeconds } from '../lib/nex/lease-ttl'
import { NexApiError } from '../lib/nex/types'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { useHostStore } from '../stores/useHostStore'

export const LEASE_IDLE_MULTIPLIER = 2
export const LEASE_MIN_REMAINING_MS = 5000

export interface ExecutionLeaseApi {
  ensureLease(): Promise<string>
  release(): Promise<void>
  touch(): void
}

export function useExecutionLease(hostId: string, executionId: string): ExecutionLeaseApi {
  const key = executionKey(hostId, executionId)
  const inflight = useRef<Promise<string> | null>(null)
  const lastActivity = useRef<number>(Date.now())
  const timer = useRef<ReturnType<typeof setInterval> | null>(null)
  const ttlMs = useRef<number>(120_000)
  // disposed: unmount or host removal happened — no async continuation may
  // write the store again. releasing: a release() is under way — an
  // in-flight renew/attach that resolves afterwards must not resurrect it.
  const disposed = useRef(false)
  const releasing = useRef(false)

  const stopTimer = useCallback(() => {
    if (timer.current) { clearInterval(timer.current); timer.current = null }
  }, [])

  const writeLease = useCallback((lease: { leaseId: string; expiresAt: number } | null) => {
    if (disposed.current) return
    useExecutionStore.getState().setLease(hostId, executionId, lease)
  }, [hostId, executionId])

  const startTimer = useCallback(() => {
    stopTimer()
    timer.current = setInterval(async () => {
      const cur = useExecutionStore.getState().executions[key]?.lease
      if (!cur || disposed.current) { stopTimer(); return }
      if (Date.now() - lastActivity.current > ttlMs.current * LEASE_IDLE_MULTIPLIER) {
        // Idle policy: stop heart-beating and let the server expire it.
        stopTimer()
        return
      }
      // Nexen mints a new lease id on every attach. Capture the id this tick
      // is renewing so a stale continuation (this call hung past a
      // subsequent re-attach) can be detected once it settles.
      const id = cur.leaseId
      try {
        const r = await renewLease(hostId, executionId, id)
        const stillCurrent = useExecutionStore.getState().executions[key]?.lease?.leaseId === id
        if (!releasing.current && stillCurrent) writeLease({ leaseId: r.lease_id, expiresAt: r.expires_at })
      } catch (e) {
        if (e instanceof NexApiError && (e.code === 'lease_expired' || e.code === 'lease_mismatch')) {
          // A stale expired/mismatch for an id nobody holds any more must
          // not wipe out — or stop the timer for — whatever lease is
          // current now (e.g. a fresh re-attach that raced this renew).
          const stillCurrent = useExecutionStore.getState().executions[key]?.lease?.leaseId === id
          if (stillCurrent) {
            writeLease(null)
            stopTimer()
          }
        }
        // anything else: keep trying at the same cadence
      }
    }, Math.max(1000, ttlMs.current / 3))
  }, [hostId, executionId, key, writeLease, stopTimer])

  const ensureLease = useCallback((): Promise<string> => {
    // The host may be gone even though `disposed` is still false (that flag
    // also covers plain unmount) — check the store directly so a removed
    // host can never be attached to (getDaemonBase silently falls back to
    // the active host for an unknown hostId, which would hit the wrong
    // daemon).
    if (!useHostStore.getState().hosts[hostId]) {
      return Promise.reject(new NexApiError(0, 'host_removed', 'host removed'))
    }
    const cur = useExecutionStore.getState().executions[key]?.lease
    if (cur && cur.expiresAt - Date.now() > LEASE_MIN_REMAINING_MS) {
      // The idle policy may have stopped the heartbeat while this lease was
      // still valid; reusing it here must re-arm the timer, not just hand
      // back the id.
      if (!timer.current) {
        lastActivity.current = Date.now()
        startTimer()
      }
      return Promise.resolve(cur.leaseId)
    }
    if (inflight.current) return inflight.current
    releasing.current = false
    const p = (async () => {
      try {
        ttlMs.current = (await getLeaseTtlSeconds(hostId)) * 1000
        const r = await attachControl(hostId, executionId)
        if (disposed.current || releasing.current) {
          // Acquired for nobody: give it straight back rather than leave a
          // lease the pane will never renew.
          void releaseLease(hostId, executionId, r.lease_id).catch(() => {})
          throw new NexApiError(0, 'lease_abandoned', 'pane went away while acquiring the lease')
        }
        writeLease({ leaseId: r.lease_id, expiresAt: r.expires_at })
        useExecutionStore.getState().setLeaseError(hostId, executionId, null)
        lastActivity.current = Date.now()
        startTimer()
        return r.lease_id
      } catch (e) {
        if (e instanceof NexApiError && !disposed.current) {
          const heldBy = useExecutionStore.getState().executions[key]?.summary?.lease?.principal_id
          useExecutionStore.getState().setLeaseError(hostId, executionId, { code: e.code, heldBy })
        }
        throw e
      } finally {
        inflight.current = null
      }
    })()
    inflight.current = p
    return p
  }, [hostId, executionId, key, writeLease, startTimer])

  const release = useCallback(async () => {
    releasing.current = true
    stopTimer()
    const cur = useExecutionStore.getState().executions[key]?.lease
    if (!cur) return
    useExecutionStore.getState().setLease(hostId, executionId, null)
    try { await releaseLease(hostId, executionId, cur.leaseId) } catch { /* best-effort */ }
  }, [hostId, executionId, key, stopTimer])

  const touch = useCallback(() => { lastActivity.current = Date.now() }, [])

  // Host removal (keep-tabs mode, spec §4.3.4): the daemon is gone, so drop
  // local authority and stop the heartbeat without a release call.
  // useHostStore has no subscribeWithSelector — compare prev/next by hand.
  useEffect(() => {
    return useHostStore.subscribe((state, prev) => {
      if (prev.hosts[hostId] && !state.hosts[hostId]) {
        disposed.current = true
        stopTimer()
        useExecutionStore.getState().setLease(hostId, executionId, null)
      }
    })
  }, [hostId, executionId, stopTimer])

  // Teardown: unmount / execution change → release once if held. beforeunload
  // gets a keepalive fetch because the page is going away.
  useEffect(() => {
    disposed.current = false
    const onUnload = () => {
      const cur = useExecutionStore.getState().executions[key]?.lease
      if (!cur) return
      void releaseLease(hostId, executionId, cur.leaseId, { keepalive: true }).catch(() => {})
    }
    window.addEventListener('beforeunload', onUnload)
    return () => {
      window.removeEventListener('beforeunload', onUnload)
      disposed.current = true
      void release()
    }
  }, [hostId, executionId, key, release])

  return { ensureLease, release, touch }
}
