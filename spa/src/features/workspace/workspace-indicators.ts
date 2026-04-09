import type { Tab } from '../../types/tab'
import type { AgentStatus } from '../../stores/useAgentStore'
import { getPrimaryPane } from '../../lib/pane-tree'
import { compositeKey } from '../../lib/composite-key'

/** Extract compositeKeys from a workspace's tab IDs. Skips non-session and missing tabs. */
export function getWorkspaceCompositeKeys(tabIds: string[], tabs: Record<string, Tab>): string[] {
  const keys: string[] = []
  for (const id of tabIds) {
    const tab = tabs[id]
    if (!tab) continue
    const { content } = getPrimaryPane(tab.layout)
    if (content.kind !== 'tmux-session') continue
    keys.push(compositeKey(content.hostId, content.sessionCode))
  }
  return keys
}

const STATUS_PRIORITY: Record<AgentStatus, number> = {
  error: 3,
  waiting: 2,
  running: 1,
  idle: 0,
}

/** Returns highest-priority status across tabs, or undefined if all idle/absent. */
export function aggregateStatus(statuses: (AgentStatus | undefined)[]): AgentStatus | undefined {
  let highest: AgentStatus | undefined
  let highestPri = -1
  for (const s of statuses) {
    if (s === undefined) continue
    const p = STATUS_PRIORITY[s]
    if (p > highestPri) {
      highest = s
      highestPri = p
    }
  }
  return highest === 'idle' ? undefined : highest
}
