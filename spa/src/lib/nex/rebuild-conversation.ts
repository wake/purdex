// spa/src/lib/nex/rebuild-conversation.ts — 重建… on an ended conversation row (Settings → Worker → 已退出 / 測試用).
import { openConversationRebuild } from './open-conversation-rebuild'
import { openWorkerTab } from '../../features/workspace/lib/open-worker-tab'
import type { ConversationRow } from './conversations-api'

/** 重建… on an ended row: the latest stint's exited screen for a worker-last row that has one, else the rebuild tab. */
export function rebuildConversation(hostId: string, row: ConversationRow): void {
  if (row.last_in === 'worker' && row.latest_execution_id) {
    openWorkerTab({ kind: 'execution', executionId: row.latest_execution_id, host: hostId })
    return
  }
  // Never rejects: the host-config load and the home lookup it waits on swallow their own failures.
  void openConversationRebuild(hostId, row)
}
