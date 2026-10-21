// spa/src/lib/watch/open-queue.ts — the way into the job queue from a tab's hourglass (M34, M40).
// One place for the three ways to take it (a click on the symbol, Alt+Enter on the tab, the tab's context menu row), so they cannot drift apart.
import { openWatchArea } from '../../features/workspace/lib/open-watch-area'
import { jobKey, writeJobsMode, writeSelectedJob } from '../pool/jobs-view-memory'
import type { AgentStatus } from '../../stores/useAgentStore'
import type { TabIndicatorStyle } from '../../stores/useUISettingsStore'
import type { QueuedItem } from './queued-sessions'

/** The action behind the hourglass: the live job queue with the first pool job selected. undefined when there is none. */
export function queueOpener(waiters: QueuedItem[] | undefined): (() => void) | undefined {
  const first = waiters?.[0]
  if (!first) return undefined
  return () => { writeJobsMode('live'); writeSelectedJob(jobKey(first.origin, first.jobId)); openWatchArea({ tab: 'jobs' }) }
}

interface TabHourglass {
  backgroundWaiters: QueuedItem[] | undefined
  tabIndicatorStyle: TabIndicatorStyle
  agentStatus: AgentStatus | undefined
  isAwaitingApproval: boolean
}

/** What a tab offers by keyboard (Alt+Enter, the context menu row): the opener, but only while the hourglass is actually drawn.
 *  TabIcon / renderInlineTabIcon draw none without a light (no status) or with the lights off ('icon'); a control for a symbol the user cannot see would surprise them (#3806 attack finding). */
export function tabQueueOpener({ backgroundWaiters, tabIndicatorStyle, agentStatus, isAwaitingApproval }: TabHourglass): (() => void) | undefined {
  if (tabIndicatorStyle === 'icon' || (!agentStatus && !isAwaitingApproval)) return undefined
  return queueOpener(backgroundWaiters)
}
