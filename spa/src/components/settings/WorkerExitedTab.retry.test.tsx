// Settings → Worker → 已退出 with the real conversations hook (WorkerExitedTab.test.tsx mocks it): what the error
// line, the retry and the loading feedback do against the hook's actual phases (R-4-12: mount and retry only), and
// that a retry's loading feedback does not depend on the list being empty (PR #1630 A1).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'

const listConversations = vi.fn()
vi.mock('../../lib/nex/conversations-api', () => ({ listConversations: (...a: unknown[]) => listConversations(...a) }))
import { WorkerExitedTab } from './WorkerExitedTab'
import { HandoffApiError } from '../../lib/nex/handoff-api'
import type { ConversationRow, ConversationsPage } from '../../lib/nex/conversations-api'

const row = (o: Partial<ConversationRow>): ConversationRow => ({
  session_id: 's', title: 't', title_source: 'ai', cwd: '/w/proj', cwd_exists: true,
  last_activity_at: Date.now(), last_in: 'terminal', ...o,
})
const page = (rows: ConversationRow[]): ConversationsPage => ({
  state: 'ended', scanned_at: 1, home: '/Users/wake', total: rows.length, truncated: false, unknown_owner: 0, conversations: rows,
})
function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((a) => { resolve = a })
  return { promise, resolve }
}
const flush = () => act(async () => { await Promise.resolve() })

describe('WorkerExitedTab retry (real hook)', () => {
  beforeEach(() => { listConversations.mockReset() })

  it('a failed load shows the error code and retry; a retry shows the loading line until its answer, then the rows', async () => {
    listConversations.mockRejectedValueOnce(new HandoffApiError(503, 'conversations_unavailable', {}, 'down'))
    render(<WorkerExitedTab hostId="h1" />)
    await flush()
    expect(listConversations).toHaveBeenCalledWith('h1', 'ended', 'normal')
    expect(screen.getByTestId('worker-exited-error')).toHaveTextContent('conversations_unavailable')
    expect(screen.queryByTestId('worker-exited-loading')).toBeNull()

    const d = deferred<ConversationsPage>()
    listConversations.mockReturnValueOnce(d.promise)
    fireEvent.click(screen.getByTestId('worker-exited-retry'))
    expect(listConversations).toHaveBeenCalledTimes(2)
    expect(screen.queryByTestId('worker-exited-error')).toBeNull()
    expect(screen.getByTestId('worker-exited-loading')).toHaveAttribute('aria-busy', 'true')

    await act(async () => { d.resolve(page([row({ session_id: 's1', title: 'one' }), row({ session_id: 's2', title: 'two' })])) })
    expect(screen.queryByTestId('worker-exited-loading')).toBeNull()
    expect(screen.getAllByTestId('conversation-row')).toHaveLength(2)
    expect(screen.getByRole('list')).not.toHaveAttribute('aria-busy')
  })

  it('a 404 (Nexen disabled) shows the unavailable notice, not the error line', async () => {
    listConversations.mockRejectedValueOnce(new HandoffApiError(404, 'http_404', {}, 'not found'))
    render(<WorkerExitedTab hostId="h1" />)
    await flush()
    expect(screen.getByTestId('worker-exited-unavailable')).toBeInTheDocument()
    expect(screen.queryByTestId('worker-exited-error')).toBeNull()
  })

  it('fetches once on mount and not again on its own', async () => {
    listConversations.mockResolvedValue(page([row({ session_id: 's1', title: 'one' })]))
    const { rerender } = render(<WorkerExitedTab hostId="h1" />)
    await flush()
    rerender(<WorkerExitedTab hostId="h1" />)
    await flush()
    expect(listConversations).toHaveBeenCalledTimes(1)
    expect(screen.getAllByTestId('conversation-row')).toHaveLength(1)
  })
})
