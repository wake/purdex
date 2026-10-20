// spa/src/lib/watch/open-queue.ts — the way into the job queue from a tab's hourglass (M34, M40).
// One place for the three ways to take it (a click on the symbol, Alt+Enter on the tab, the tab's context menu row), so they cannot drift apart.
import { openWatchArea } from '../../features/workspace/lib/open-watch-area'
import { jobKey, writeJobsMode, writeSelectedJob } from '../pool/jobs-view-memory'
import { isJobMark, type QueuedItem } from './queued-sessions'

/** The action behind the hourglass: the live job queue with the first pool job selected. undefined when the waiters hold no pool job (gate waiters alone are not a way in). */
export function queueOpener(waiters: QueuedItem[] | undefined): (() => void) | undefined {
  const first = waiters?.find(isJobMark)
  if (!first) return undefined
  return () => { writeJobsMode('live'); writeSelectedJob(jobKey(first.origin, first.jobId)); openWatchArea({ tab: 'jobs' }) }
}
