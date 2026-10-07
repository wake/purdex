// Worker test tab spec §7: a host whose daemon lacks `conversations.scope.v1` behaves exactly as before. The daemon
// ignores `?scope=`, so the Workers / 已退出 / 已消失 tabs show every row it returns (nothing is filtered client-side),
// and 測試用 explains itself without calling anything.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'

const listConversations = vi.fn()
vi.mock('../../lib/nex/conversations-api', () => ({ listConversations: (...a: unknown[]) => listConversations(...a) }))
const listExecutions = vi.fn()
vi.mock('../../lib/nex/nex-api', () => ({ listExecutions: (...a: unknown[]) => listExecutions(...a), attachControl: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), archiveExecution: vi.fn() }))
vi.mock('../../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))
import { WorkerLiveTab } from './WorkerLiveTab'
import { WorkerExitedTab } from './WorkerExitedTab'
import { WorkerGoneTab } from './WorkerGoneTab'
import { WorkerTestTab } from './WorkerTestTab'
import { resetExecutionListForTests, useExecutionListStore } from '../../stores/useExecutionListStore'
import { useNexHostStore, type NexHostEntry } from '../../stores/useNexHostStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { subscriptionSlots } from '../../lib/nex/subscription-slots'
import type { ConversationRow, ConversationsPage } from '../../lib/nex/conversations-api'
import type { ExecutionSummary } from '../../lib/nex/types'

const H = 'h1'
const crow = (o: Partial<ConversationRow>): ConversationRow => ({
  session_id: 's', title: 't', title_source: 'ai', cwd: '/w/proj', cwd_exists: true, last_activity_at: Date.now(), last_in: 'terminal', ...o,
})
const page = (state: 'ended' | 'gone', rows: ConversationRow[]): ConversationsPage => ({
  state, scanned_at: 1, home: '/Users/wake', total: rows.length, truncated: false, unknown_owner: 0, conversations: rows,
})
const erow = (o: Partial<ExecutionSummary> & { id: string }): ExecutionSummary =>
  ({ state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'brief', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false, ...o }) as ExecutionSummary
const flush = () => act(async () => { await Promise.resolve() })

const legacyEntry: NexHostEntry = {
  info: { configured: true, mounted: true, ready: true, init_error: '', effective: null },
  capabilities: null, phase: 'ready', error: null, fetchedAt: 1, generation: 1, fingerprint: '1:1:t', daemonCapabilities: ['transcript.v1'],
}

describe('a daemon without conversations.scope.v1', () => {
  beforeEach(() => {
    listConversations.mockReset()
    listExecutions.mockReset().mockResolvedValue({ items: [], next_cursor: '' })
    subscriptionSlots.resetForTests()
    resetExecutionListForTests()
    useShownHostsStore.setState({ ids: [H] })
    useNexHostStore.setState({ byHost: { [H]: legacyEntry }, ensure: vi.fn().mockResolvedValue(undefined) })
    useExecutionListStore.setState({
      byHost: { [H]: { items: [
        erow({ id: 'N1', session_id: 'SN', cwd: '/Users/w/proj', brief: 'normal one' }),
        erow({ id: 'T1', session_id: 'ST', cwd: '/tmp/x', brief: 'test one' }),
      ], phase: 'ready', error: null, lastSeq: null, refreshRevision: 0, truncated: false, complete: true } },
    })
  })

  it('Workers lists every running row, test cwd included', () => {
    render(<WorkerLiveTab hostId={H} />)
    expect(screen.getAllByTestId('executions-row')).toHaveLength(2)
    expect(screen.getByText('test one')).toBeInTheDocument()
  })

  it('已退出 renders every row the daemon returns (the request carries scope=normal, the response is not re-filtered)', async () => {
    listConversations.mockResolvedValue(page('ended', [crow({ session_id: 'a', title: 'normal chat' }), crow({ session_id: 'b', title: 'tmp chat', cwd: '/tmp/x' })]))
    render(<WorkerExitedTab hostId={H} />)
    await flush()
    expect(listConversations).toHaveBeenCalledWith(H, 'ended', 'normal')
    expect(screen.getAllByTestId('conversation-row')).toHaveLength(2)
    expect(screen.getByText('tmp chat')).toBeInTheDocument()
  })

  it('已消失 renders every row the daemon returns', async () => {
    listConversations.mockResolvedValue(page('gone', [crow({ session_id: 'a', title: 'normal chat' }), crow({ session_id: 'b', title: 'tmp chat', cwd: '/tmp/x' })]))
    render(<WorkerGoneTab hostId={H} />)
    await flush()
    expect(listConversations).toHaveBeenCalledWith(H, 'gone', 'normal')
    expect(screen.getAllByTestId('conversation-row')).toHaveLength(2)
  })

  it('測試用 only explains: no conversations request, no execution list request', async () => {
    render(<WorkerTestTab hostId={H} />)
    await flush()
    expect(screen.getByTestId('worker-test-unsupported')).toBeInTheDocument()
    expect(listConversations).not.toHaveBeenCalled()
    expect(listExecutions).not.toHaveBeenCalled()
  })
})
