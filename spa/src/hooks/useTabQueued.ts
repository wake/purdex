// spa/src/hooks/useTabQueued.ts — the panes of a tab whose agent waits in a host's command queue (watcher-area plan §8), or whose session has a pool job queued, claimed or running (WWD-7b1-6).
import { useMemo } from 'react'
import { useFleetStore } from '../lib/fleet/store'
import type { FleetWaiter } from '../lib/fleet/types'
import { useJobsStore } from '../lib/pool/jobs-store'
import { usePoolStore } from '../lib/pool/store'
import type { TabAgentPane } from '../lib/agent-lights/tab-aggregate'
import { queuedBySession, queuedJobsBySession, type QueuedJobMark } from '../lib/watch/queued-sessions'

export interface TabQueued {
  /** Keys of the panes whose agent session has a waiter or a live pool job. */
  keys: ReadonlySet<string>
  /** Every gate waiter of those panes (until WWD-9 removes the gate). */
  waiters: FleetWaiter[]
  /** Every live pool job of those panes' sessions. */
  jobs: QueuedJobMark[]
}

const NONE: TabQueued = { keys: new Set(), waiters: [], jobs: [] }

/**
 * The selectors return JSON strings (primitives), so a fleet push or a jobs read that changes nothing for this tab does not re-render
 * it, and a Map built per call never leaks into the render.
 */
export function useTabQueued(panes: TabAgentPane[]): TabQueued {
  const sig = useFleetStore((s) => {
    const withSession = panes.filter((p) => p.sessionId)
    // Spec 2.3: no fleet (off, no primary, or the primary silent) → no hourglass; the store keeps the last snapshot.
    if (withSession.length === 0 || !s.snapshot || s.primaryHostId === null || s.stale || s.waitingPrimary) return ''
    const bySession = queuedBySession(s.snapshot)
    if (bySession.size === 0) return ''
    const hits = withSession.flatMap((p) => {
      const waiters = bySession.get(p.sessionId!.toLowerCase())
      return waiters ? [{ key: p.key, waiters }] : []
    })
    return hits.length > 0 ? JSON.stringify(hits) : ''
  })
  // A queued job's place comes from the display order, which reads each origin's zone (+200 warning, +400 danger): the hosts load after the jobs, and a zone moves. This subscribes to exactly that, as a string, so the selector
  // below (which reads the hosts fresh on every run) is run again when a zone changes, and nothing else of the pool re-renders the tab.
  usePoolStore((s) => s.hosts.map((h) => `${h.alias}:${h.facts?.derived.zone ?? ''}`).join('|'))
  // The pool's jobs: matched by the session that submitted them (`submitter.session`); an empty one (a job from before #3477) lights nothing.
  const jobsSig = useJobsStore((s) => {
    const withSession = panes.filter((p) => p.sessionId)
    if (withSession.length === 0) return ''
    const mine = new Set(withSession.map((p) => p.sessionId!.toLowerCase()))
    const all = Object.values(s.byOrigin).flatMap((o) => Object.values(o.jobs))
    // cheap first: the display order (a sort over every queued job) is only worked out for a tab that has a live job at all
    if (!all.some((j) => (j.state === 'queued' || j.state === 'claimed' || j.state === 'running') && mine.has(j.submitter.session.toLowerCase()))) return ''
    const bySession = queuedJobsBySession(all, Date.now(), usePoolStore.getState().hosts)
    const hits = withSession.flatMap((p) => {
      const jobs = bySession.get(p.sessionId!.toLowerCase())
      return jobs ? [{ key: p.key, jobs }] : []
    })
    return hits.length > 0 ? JSON.stringify(hits) : ''
  })
  return useMemo(() => {
    if (!sig && !jobsSig) return NONE
    const waiterHits = (sig ? JSON.parse(sig) : []) as { key: string; waiters: FleetWaiter[] }[]
    const jobHits = (jobsSig ? JSON.parse(jobsSig) : []) as { key: string; jobs: QueuedJobMark[] }[]
    return {
      keys: new Set([...waiterHits.map((h) => h.key), ...jobHits.map((h) => h.key)]),
      waiters: waiterHits.flatMap((h) => h.waiters),
      jobs: jobHits.flatMap((h) => h.jobs),
    }
  }, [sig, jobsSig])
}
