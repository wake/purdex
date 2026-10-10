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
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

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

  // #1952 (as 已退出, #1627 C): a keyboard retry keeps keyboard focus. The button stays mounted (disabled, aria-busy) while
  // the retry runs; a failure keeps focus on it, a success moves it to the list — only when the button had focus at the
  // press and focus has not gone elsewhere since.
  describe('focus', () => {
    const failed = async () => {
      listConversations.mockRejectedValueOnce(new HandoffApiError(503, 'conversations_unavailable', {}, 'down'))
      render(<WorkerGoneTab hostId="h1" />)
      await flush()
      const d = deferred<ConversationsPage>()
      listConversations.mockReturnValueOnce(d.promise)
      return d
    }
    const retryBtn = () => screen.getByTestId('worker-gone-retry')
    // Enter / Space on a focused button is a click on it.
    const pressByKeyboard = () => { retryBtn().focus(); fireEvent.click(retryBtn()) }

    it('the button stays, busy, while the retry runs; a failure keeps focus on it', async () => {
      const d = await failed()
      pressByKeyboard()
      const btn = retryBtn()
      expect(btn).toBeDisabled()
      expect(btn).toHaveAttribute('aria-busy', 'true')
      // A browser drops focus from a button that turns disabled; jsdom keeps it, so what proves the failure path is that
      // the button is focused again explicitly.
      const refocus = vi.spyOn(btn, 'focus')
      await act(async () => { d.reject(new HandoffApiError(503, 'still_down', {}, 'down')) })
      expect(retryBtn()).toBe(btn)
      expect(btn).toBeEnabled()
      expect(screen.getByTestId('worker-gone-error')).toHaveTextContent('still_down')
      expect(refocus).toHaveBeenCalled()
      expect(document.activeElement).toBe(btn)
    })

    it('a success moves focus to the list', async () => {
      const d = await failed()
      pressByKeyboard()
      await act(async () => { d.resolve(page([row({ session_id: 's1', title: 'one' })])) })
      expect(screen.queryByTestId('worker-gone-retry')).toBeNull()
      expect(document.activeElement).toBe(screen.getByRole('list'))
    })

    it('a success with no rows moves focus to the empty line', async () => {
      const d = await failed()
      pressByKeyboard()
      await act(async () => { d.resolve(page([])) })
      expect(document.activeElement).toBe(screen.getByTestId('worker-gone-empty'))
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
      const search = screen.getByTestId('worker-gone-search')
      search.focus()
      await act(async () => { d.resolve(page([row({ session_id: 's1', title: 'one' })])) })
      expect(document.activeElement).toBe(search)
    })
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
