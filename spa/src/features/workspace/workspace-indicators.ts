import type { Tab } from '../../types/tab'
import type { AgentStatus } from '../../stores/useAgentStore'
import { tabAgentPanes } from '../../lib/agent-lights/tab-aggregate'
import { statusRank } from '../../lib/agent-lights/status-rank'

/**
 * The agent keys of each of a workspace's tabs, one array per tab: every agent pane of the tab's layout (U1-3
 * ruling 6), so a split tab's second pane counts. Skips missing tabs and tabs with no agent pane.
 */
export function getWorkspaceTabKeys(tabIds: string[], tabs: Record<string, Tab>): string[][] {
  const out: string[][] = []
  for (const id of tabIds) {
    const tab = tabs[id]
    if (!tab) continue
    const keys = tabAgentPanes(tab.layout).map((p) => p.key)
    if (keys.length > 0) out.push(keys)
  }
  return out
}

export type ActiveStatus = Exclude<AgentStatus, 'idle'>

/** Returns highest-priority status across tabs, or undefined if all idle/absent. */
export function aggregateStatus(statuses: (AgentStatus | undefined)[]): ActiveStatus | undefined {
  let highest: AgentStatus | undefined
  let highestRank = 0
  for (const s of statuses) {
    const r = statusRank(s)
    if (r > highestRank) {
      highest = s
      highestRank = r
    }
  }
  return highest === 'idle' ? undefined : highest
}
