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
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((a, b) => { resolve = a; reject = b })
  return { promise, resolve, reject }
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
    // #1627 C: the error line and its button stay while the retry runs (the button busy); the loading line is a status.
    expect(screen.getByTestId('worker-exited-error')).toHaveTextContent('conversations_unavailable')
    expect(screen.getByTestId('worker-exited-loading')).toHaveAttribute('role', 'status')

    await act(async () => { d.resolve(page([row({ session_id: 's1', title: 'one' }), row({ session_id: 's2', title: 'two' })])) })
    expect(screen.queryByTestId('worker-exited-loading')).toBeNull()
    expect(screen.queryByTestId('worker-exited-error')).toBeNull()
    expect(screen.getAllByTestId('conversation-row')).toHaveLength(2)
    expect(screen.getByRole('list')).not.toHaveAttribute('aria-busy')
  })

  // #1627 C: a keyboard retry keeps keyboard focus. The button stays mounted (disabled, aria-busy) while the retry
  // runs; a failure keeps focus on it, a success moves it to the list — only when the button had focus at the press
  // and focus has not gone elsewhere since.
  describe('focus', () => {
    const failed = async () => {
      listConversations.mockRejectedValueOnce(new HandoffApiError(503, 'conversations_unavailable', {}, 'down'))
      render(<WorkerExitedTab hostId="h1" />)
      await flush()
      const d = deferred<ConversationsPage>()
      listConversations.mockReturnValueOnce(d.promise)
      return d
    }
    const retryBtn = () => screen.getByTestId('worker-exited-retry')
    // Enter / Space on a focused button is a click on it.
    const pressByKeyboard = () => { retryBtn().focus(); fireEvent.click(retryBtn()) }

    it('the button stays, busy, while the retry runs; a failure keeps focus on it', async () => {
      const d = await failed()
      pressByKeyboard()
      const btn = retryBtn()
      expect(btn).toBeDisabled()
      expect(btn).toHaveAttribute('aria-busy', 'true')
      // A browser drops focus from a button that turns disabled (ConfirmDialog's note); jsdom keeps it, so what proves
      // the failure path is that it focuses the button again itself.
      const refocus = vi.spyOn(btn, 'focus')
      await act(async () => { d.reject(new HandoffApiError(503, 'still_down', {}, 'down')) })
      expect(retryBtn()).toBe(btn)
      expect(btn).toBeEnabled()
      expect(btn).toHaveAttribute('aria-busy', 'false')
      expect(screen.getByTestId('worker-exited-error')).toHaveTextContent('still_down')
      expect(refocus).toHaveBeenCalled()
      expect(document.activeElement).toBe(btn)
    })

    it('a success moves focus to the list', async () => {
      const d = await failed()
      pressByKeyboard()
      await act(async () => { d.resolve(page([row({ session_id: 's1', title: 'one' })])) })
      expect(screen.queryByTestId('worker-exited-retry')).toBeNull()
      expect(document.activeElement).toBe(screen.getByRole('list'))
    })

    it('a press without focus on the button (a pointer that does not focus it) moves no focus', async () => {
      const d = await failed()
      expect(document.activeElement).toBe(document.body)
      fireEvent.click(retryBtn())
      await act(async () => { d.resolve(page([row({ session_id: 's1', title: 'one' })])) })
      expect(document.activeElement).toBe(document.body)
    })

    it('focus moved elsewhere while the retry ran is left alone', async () => {
      const d = await failed()
      pressByKeyboard()
      const search = screen.getByTestId('worker-exited-search')
      search.focus()
      await act(async () => { d.resolve(page([row({ session_id: 's1', title: 'one' })])) })
      expect(document.activeElement).toBe(search)
    })
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
