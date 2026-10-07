// spa/src/stores/useWorkerTitlePrefetchStore.ts — summaries fetched only to name a worker (execution) tab whose
// execution has neither a live summary nor a host list row (#1557: an archived worker is not on the host's default
// list page, so after a reload its unopened tab had nothing to be titled from). Written by
// `lib/nex/worker-title-prefetch.ts`; read as the LAST fallback by `lib/nex/worker-summary.ts`. A one-shot snapshot
// nothing refreshes: it names a tab, it never says what a worker is doing — the pane's subscription and the agent
// projection never read it. Module-level, so it outlives the pane (CLAUDE.md, tab-hosted checklist). Not persisted.
import { create } from 'zustand'
import type { ExecutionSummary } from '../lib/nex/types'

interface WorkerTitlePrefetchState {
  /** By `executionKey(hostId, executionId)`. */
  byKey: Record<string, ExecutionSummary>
}

export const useWorkerTitlePrefetchStore = create<WorkerTitlePrefetchState>()(() => ({ byKey: {} }))
