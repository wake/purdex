// spa/src/hooks/useTabQueued.ts — the panes of a tab whose session has a pool job queued, claimed or running (WWD-7b1-6). The old gate's waiters (watcher-area plan §8) are no longer read (MON-12a-4).
import { useMemo, useSyncExternalStore } from 'react'
import { useJobsStore } from '../lib/pool/jobs-store'
import { coarseNow, subscribeCoarseNow } from './useCoarseNow'
import { usePoolStore } from '../lib/pool/store'
import type { TabAgentPane } from '../lib/agent-lights/tab-aggregate'
import { sharedQueuedJobs, type QueuedJobMark } from '../lib/watch/queued-sessions'

export interface TabQueued {
  /** Keys of the panes whose agent session has a live pool job. */
  keys: ReadonlySet<string>
  /** Every live pool job of those panes' sessions. */
  jobs: QueuedJobMark[]
}

const NONE: TabQueued = { keys: new Set(), jobs: [] }
const NO_SUBSCRIPTION = () => () => {}

/**
 * The selector returns a JSON string (a primitive), so a jobs read that changes nothing for this tab does not re-render
 * it, and a Map built per call never leaks into the render.
 */
export function useTabQueued(panes: TabAgentPane[]): TabQueued {
  // A queued job's place comes from the display order: it reads each origin's zone (+200 warning, +400 danger), which loads after the jobs and moves, and it counts whole minutes waited. The zones are subscribed to here, as a
  // primitive, so the selector below (which reads the hosts and the minute fresh on every run) is run again when a zone changes; the minute turning is subscribed to further down, and only by a tab that has a job mark.
  usePoolStore((s) => s.hosts.map((h) => `${h.alias}:${h.facts?.derived.zone ?? ''}`).join('|'))
  // The pool's jobs: matched by the session that submitted them (`submitter.session`); an empty one (a job from before #3477) lights nothing.
  const jobsSig = useJobsStore((s) => {
    const withSession = panes.filter((p) => p.sessionId)
    if (withSession.length === 0) return ''
    // worked out once for every tab (memoised on the jobs, the zones and the minute), then a lookup per pane
    const bySession = sharedQueuedJobs(s.byOrigin, usePoolStore.getState().hosts, coarseNow())
    if (bySession.size === 0) return ''
    const hits = withSession.flatMap((p) => {
      const jobs = bySession.get(p.sessionId!.toLowerCase())
      return jobs ? [{ key: p.key, jobs }] : []
    })
    return hits.length > 0 ? JSON.stringify(hits) : ''
  })
  // A tab with a job mark re-renders each minute (the place counts whole minutes waited, so the selector above is run again then); a tab with none holds no per-minute timer.
  useSyncExternalStore(jobsSig ? subscribeCoarseNow : NO_SUBSCRIPTION, coarseNow, coarseNow)
  return useMemo(() => {
    if (!jobsSig) return NONE
    const jobHits = JSON.parse(jobsSig) as { key: string; jobs: QueuedJobMark[] }[]
    return { keys: new Set(jobHits.map((h) => h.key)), jobs: jobHits.flatMap((h) => h.jobs) }
  }, [jobsSig])
}
