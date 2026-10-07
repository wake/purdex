// spa/src/lib/nex/worker-title-prefetch.ts — a title for a worker tab that has nothing to be titled from (#1557).
//
// A worker (execution) tab is titled from its execution's summary: the live pane state, else the host's list row
// (worker-summary.ts). The list asks for unarchived executions only (execution-list-effects.ts), and a pane is mounted
// only while its tab is selected, so after a reload an archived worker's unopened tab had neither and fell back to the
// pane label 「執行」 until it was selected. This fetches that one execution's summary instead, into
// `useWorkerTitlePrefetchStore`, which worker-summary.ts reads as the last fallback.
//
// Asked only when the answer is known to be missing: the host is configured, its Nexen is ready, and its list has
// answered (phase `ready`) without the row — the list subscription itself is the worker projection's, held for every
// host with a worker tab (useWorkerAgentProjection.ts). Bounded:
// - once per (host, execution) per session, success or failure: a 404 or a network error keeps the pane label and is
//   never retried (a host removed or re-pointed is forgotten, answers and marks: they came from the old daemon);
// - one request in flight per host, so of the two REST lanes subscription-slots.ts keeps free for lease renew / send
//   this never takes both.
// An answer is dropped when its host was removed or re-pointed meanwhile, or when a live summary or a list row turned
// up for the execution — those are fresher, and they win the read anyway.
//
// Started once from main.tsx (app lifetime, outside React, so StrictMode cannot double the requests).
import { executionKey, splitExecutionKey, useExecutionStore } from '../../stores/useExecutionStore'
import { useExecutionListStore } from '../../stores/useExecutionListStore'
import { useHostStore } from '../../stores/useHostStore'
import { selectReady, useNexHostStore } from '../../stores/useNexHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkerTitlePrefetchStore } from '../../stores/useWorkerTitlePrefetchStore'
import { scanPaneTree } from '../pane-tree'
import { getExecution } from './nex-api'
import { fingerprintOf } from './nex-host-effects'
import { hostFingerprint } from './nex-host-reducer'
import { resolveExecutionHostId } from './resolve-host'
import { liveWorkerSummary, rowWorkerSummary } from './worker-summary'

interface WorkerRef { hostId: string; executionId: string }

// Module-level: "once per session" must hold across a stop/start, and an answer in flight across one still lands.
/** `executionKey`s whose summary was requested — never again this session (unless their host is re-pointed). */
const requested = new Set<string>()
/** Hosts with a request in flight. */
const busy = new Set<string>()
/** Bumped by the test reset, so a request from before it cannot touch the state after it. */
let epoch = 0
/** The running instance's pass, re-run when a request settles (frees its host). */
let rescan: (() => void) | null = null

/** Every execution pane in any tab — a secondary pane is named by its worker title too (pane-display-label.ts). */
function openWorkers(): WorkerRef[] {
  const out = new Map<string, WorkerRef>()
  for (const tab of Object.values(useTabStore.getState().tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      if (c.kind !== 'execution') return
      // Same resolution as the pane and the tab: a present hint verbatim, else the first host.
      const hostId = resolveExecutionHostId(c.host)
      if (hostId) out.set(executionKey(hostId, c.executionId), { hostId, executionId: c.executionId })
    })
  }
  return [...out.values()]
}

/** A live summary or a list row: what the tab reads first, and fresher than any answer of ours. */
function summaryKnown({ hostId, executionId }: WorkerRef): boolean {
  return !!liveWorkerSummary(useExecutionStore.getState().executions, hostId, executionId)
    || !!rowWorkerSummary(useExecutionListStore.getState().byHost, hostId, executionId)
}

/** Neither, although the list has answered, on a configured host whose Nexen is ready. */
function untitled(w: WorkerRef): boolean {
  if (!useHostStore.getState().hosts[w.hostId]) return false
  if (!selectReady(w.hostId)(useNexHostStore.getState())) return false
  if (useExecutionListStore.getState().byHost[w.hostId]?.phase !== 'ready') return false
  return !summaryKnown(w)
}

function request(w: WorkerRef): void {
  const key = executionKey(w.hostId, w.executionId)
  const at = epoch
  const fingerprint = fingerprintOf(w.hostId)
  requested.add(key)
  busy.add(w.hostId)
  getExecution(w.hostId, w.executionId)
    .then((summary) => {
      if (at !== epoch || fingerprintOf(w.hostId) !== fingerprint || summaryKnown(w)) return
      useWorkerTitlePrefetchStore.setState((s) => ({ byKey: { ...s.byKey, [key]: summary } }))
    })
    .catch(() => {
      // Silent: the tab keeps its pane label, as before. Not retried (see the header).
    })
    .finally(() => {
      if (at !== epoch) return
      busy.delete(w.hostId)
      rescan?.()
    })
}

/** A removed or re-pointed host: its titles and its "already asked" marks belonged to the old daemon. */
function forgetHost(hostId: string): void {
  for (const key of requested) {
    if (splitExecutionKey(key).hostId === hostId) requested.delete(key)
  }
  useWorkerTitlePrefetchStore.setState((s) => {
    const kept = Object.entries(s.byKey).filter(([key]) => splitExecutionKey(key).hostId !== hostId)
    return kept.length === Object.keys(s.byKey).length ? s : { byKey: Object.fromEntries(kept) }
  })
}

/** Start the prefetch; returns its stop. One instance at a time (main.tsx, app lifetime). */
export function startWorkerTitlePrefetch(): () => void {
  let stopped = false
  const scan = () => {
    if (stopped) return
    for (const w of openWorkers()) {
      if (busy.has(w.hostId) || requested.has(executionKey(w.hostId, w.executionId)) || !untitled(w)) continue
      request(w)
    }
  }
  rescan = scan

  // The execution store is not watched: it only ever makes a worker titled (a live summary), and it changes with
  // every streamed frame. A worker becomes untitled when its tab opens, its host's Nexen or list becomes ready, or its
  // row leaves the list (archived elsewhere).
  const unsubs = [
    useTabStore.subscribe((s, p) => { if (s.tabs !== p.tabs) scan() }),
    useExecutionListStore.subscribe((s, p) => { if (s.byHost !== p.byHost) scan() }),
    useNexHostStore.subscribe((s, p) => { if (s.byHost !== p.byHost) scan() }),
    useHostStore.subscribe((s, p) => {
      if (s.hosts !== p.hosts) {
        for (const [hostId, before] of Object.entries(p.hosts)) {
          const after = s.hosts[hostId]
          if (!after || hostFingerprint(after) !== hostFingerprint(before)) forgetHost(hostId)
        }
      }
      if (s.hosts !== p.hosts || s.hostOrder !== p.hostOrder) scan()
    }),
  ]
  scan()

  return () => {
    stopped = true
    if (rescan === scan) rescan = null
    for (const u of unsubs) u()
  }
}

/** Test seam: forget what was asked and what is in flight, and empty the store. */
export function resetWorkerTitlePrefetchForTests(): void {
  epoch += 1
  requested.clear()
  busy.clear()
  rescan = null
  useWorkerTitlePrefetchStore.setState({ byKey: {} })
}
