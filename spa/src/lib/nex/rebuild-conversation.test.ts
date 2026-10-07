import { describe, it, expect, vi, beforeEach } from 'vitest'

const openWorkerTab = vi.fn()
vi.mock('../../features/workspace/lib/open-worker-tab', () => ({ openWorkerTab: (c: unknown) => openWorkerTab(c) }))
const openConversationRebuild = vi.fn()
vi.mock('./open-conversation-rebuild', () => ({ openConversationRebuild: (h: string, r: unknown) => openConversationRebuild(h, r) }))
import { rebuildConversation } from './rebuild-conversation'
import type { ConversationRow } from './conversations-api'

const row = (o: Partial<ConversationRow>): ConversationRow => ({
  session_id: 's1', title: 't', title_source: 'ai', cwd: '/x', cwd_exists: true, last_activity_at: 0, last_in: 'terminal', ...o,
})

describe('rebuildConversation', () => {
  beforeEach(() => { openWorkerTab.mockReset(); openConversationRebuild.mockReset(); openConversationRebuild.mockResolvedValue('t') })

  it('a worker-last row with a stint opens that execution', () => {
    rebuildConversation('h', row({ last_in: 'worker', latest_execution_id: 'e9' }))
    expect(openWorkerTab).toHaveBeenCalledWith({ kind: 'execution', executionId: 'e9', host: 'h' })
    expect(openConversationRebuild).not.toHaveBeenCalled()
  })
  it('every other row opens the rebuild tab', () => {
    const terminal = row({})
    const workerNoStint = row({ last_in: 'worker' })
    rebuildConversation('h', terminal)
    rebuildConversation('h', workerNoStint)
    expect(openConversationRebuild).toHaveBeenCalledWith('h', terminal)
    expect(openConversationRebuild).toHaveBeenCalledWith('h', workerNoStint)
    expect(openWorkerTab).not.toHaveBeenCalled()
  })
})
