// spa/src/hooks/useExecutionPrelude.ts — loads a handed-off worker's prelude
// (spec §5.2): the first page as soon as the execution's own history is in,
// older pages on demand. Kept out of useExecutionSubscription — the prelude
// is immutable and has no part in its summary → history → SSE order
// contract. The store's `prelude.status === 'loading'` is the one lock, so
// two panes on the same worker never ask twice.
import { useCallback, useEffect } from 'react'
import { fetchExecutionPrelude } from '../lib/nex/nex-api'
import { NexApiError } from '../lib/nex/types'
import { executionKey, useExecutionStore } from '../stores/useExecutionStore'
import { selectTranscriptPrelude, useNexHostStore } from '../stores/useNexHostStore'

export const PRELUDE_PAGE_LIMIT = 200
/** loadAll's safety stop: far past any real transcript (a page may also hold 0 items, spec §4.3). */
export const MAX_LOAD_ALL_PAGES = 1000

/** Request ids, unique for the module's lifetime: a late answer is told apart by its id, not by the entry's status. */
let nextRequest = 1

export function useExecutionPrelude(hostId: string, executionId: string): {
  loadOlder: () => void
  loadAll: () => Promise<void>
  retry: () => void
} {
  const key = executionKey(hostId, executionId)
  const cap = useNexHostStore(selectTranscriptPrelude(hostId))
  // Spec D4: no resume id, no prelude — and no request at all.
  const eligible = useExecutionStore((s) => {
    const st = s.executions[key]
    return !!st?.historyLoaded && !!st.summary?.resume_session_id
  })
  const status = useExecutionStore((s) => s.executions[key]?.prelude.status ?? 'idle')

  /** One page; false when nothing was asked (locked, done, ended) or the answer was dropped. */
  const fetchOne = useCallback(async (): Promise<boolean> => {
    const store = useExecutionStore.getState()
    const p = store.executions[key]?.prelude
    if (!p || p.status === 'loading' || p.status === 'none' || p.status === 'gone' || p.done) return false
    const before = p.cursor
    const request = nextRequest++
    store.preludeLoading(hostId, executionId, request)
    // Only this request's own answer lands: the store actions are no-ops
    // unless `request` is still the one recorded, so an entry that was
    // cleared and recreated (with a new request in flight) never takes it.
    const ours = () => useExecutionStore.getState().executions[key]?.prelude.request === request
    try {
      const page = await fetchExecutionPrelude(hostId, executionId, { ...(before !== null ? { before } : {}), limit: PRELUDE_PAGE_LIMIT })
      if (!ours()) return false
      useExecutionStore.getState().applyPreludePage(hostId, executionId, page, before, request)
      return true
    } catch (e) {
      if (!ours()) return false
      // After this reset loadAll stops (fetchOne returns false); the effect
      // reloads the first page and the user re-triggers "load all".
      // Spec §4.2: the daemon rejected an older page's cursor (it was
      // upgraded, or the file changed): start over from the first page.
      if (before !== null && e instanceof NexApiError && e.code === 'malformed_parameter') {
        useExecutionStore.getState().resetPrelude(hostId, executionId)
        return false
      }
      useExecutionStore.getState().preludeFailed(hostId, executionId, e instanceof Error ? e.message : String(e), request)
      return false
    }
  }, [hostId, executionId, key])

  useEffect(() => {
    if (cap && eligible && status === 'idle') void fetchOne()
  }, [cap, eligible, status, fetchOne])

  const loadOlder = useCallback(() => {
    const p = useExecutionStore.getState().executions[key]?.prelude
    if (p?.status === 'ok' && !p.done) void fetchOne()
  }, [key, fetchOne])

  const retry = useCallback(() => {
    if (useExecutionStore.getState().executions[key]?.prelude.status === 'error') void fetchOne()
  }, [key, fetchOne])

  const loadAll = useCallback(async () => {
    for (let i = 0; i < MAX_LOAD_ALL_PAGES; i++) {
      const p = useExecutionStore.getState().executions[key]?.prelude
      if (!p || p.done || p.status === 'error' || p.status === 'none' || p.status === 'gone' || p.status === 'idle') return
      if (p.status === 'loading') {
        // Another caller (the sentinel) holds the lock: wait for it to settle.
        await new Promise<void>((resolve) => {
          const off = useExecutionStore.subscribe(
            (s) => s.executions[key]?.prelude.status,
            (st) => { if (st !== 'loading') { off(); resolve() } },
          )
        })
        continue
      }
      if (!(await fetchOne())) return
    }
  }, [key, fetchOne])

  return { loadOlder, loadAll, retry }
}
