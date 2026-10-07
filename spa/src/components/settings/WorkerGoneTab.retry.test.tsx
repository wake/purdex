// Settings → Worker → 已消失 with the real conversations hook (WorkerGoneTab.test.tsx mocks it): it asks for the
// gone state, fetches on mount and on retry only (R-4-12), and maps a 404 to the unavailable notice.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'

const listConversations = vi.fn()
vi.mock('../../lib/nex/conversations-api', () => ({ listConversations: (...a: unknown[]) => listConversations(...a) }))
import { WorkerGoneTab } from './WorkerGoneTab'
import { HandoffApiError } from '../../lib/nex/handoff-api'
import type { ConversationRow, ConversationsPage } from '../../lib/nex/conversations-api'

const row = (o: Partial<ConversationRow>): ConversationRow => ({
  session_id: 's', title: 't', title_source: 'ai', cwd: '/w/proj', cwd_exists: true,
  last_activity_at: Date.now(), last_in: 'terminal', ...o,
})
const page = (rows: ConversationRow[]): ConversationsPage => ({
  state: 'gone', scanned_at: 1, home: '/Users/wake', total: rows.length, truncated: false, unknown_owner: 0, conversations: rows,
})
const flush = () => act(async () => { await Promise.resolve() })

describe('WorkerGoneTab retry (real hook)', () => {
  beforeEach(() => { listConversations.mockReset() })

  it('a failed load shows the error and retry; retry fetches the gone state again and shows the rows', async () => {
    listConversations.mockRejectedValueOnce(new HandoffApiError(503, 'conversations_unavailable', {}, 'down'))
    render(<WorkerGoneTab hostId="h1" />)
    await flush()
    expect(listConversations).toHaveBeenCalledWith('h1', 'gone', 'normal')
    expect(screen.getByTestId('worker-gone-error')).toHaveTextContent('conversations_unavailable')

    listConversations.mockResolvedValueOnce(page([row({ session_id: 's1', title: 'one' })]))
    fireEvent.click(screen.getByTestId('worker-gone-retry'))
    await flush()
    expect(listConversations).toHaveBeenCalledTimes(2)
    expect(listConversations).toHaveBeenLastCalledWith('h1', 'gone', 'normal')
    expect(screen.queryByTestId('worker-gone-error')).toBeNull()
    expect(screen.getAllByTestId('conversation-row')).toHaveLength(1)
  })

  it('a 404 (Nexen disabled) shows the unavailable notice, not the error line', async () => {
    listConversations.mockRejectedValueOnce(new HandoffApiError(404, 'http_404', {}, 'not found'))
    render(<WorkerGoneTab hostId="h1" />)
    await flush()
    expect(screen.getByTestId('worker-gone-unavailable')).toBeInTheDocument()
    expect(screen.queryByTestId('worker-gone-error')).toBeNull()
  })

  it('fetches once on mount and not again on its own', async () => {
    listConversations.mockResolvedValue(page([row({ session_id: 's1', title: 'one' })]))
    const { rerender } = render(<WorkerGoneTab hostId="h1" />)
    await flush()
    rerender(<WorkerGoneTab hostId="h1" />)
    await flush()
    expect(listConversations).toHaveBeenCalledTimes(1)
    expect(screen.getAllByTestId('conversation-row')).toHaveLength(1)
  })
})
