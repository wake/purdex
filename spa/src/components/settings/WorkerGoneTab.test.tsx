import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, within, act } from '@testing-library/react'

const conversations = vi.fn()
vi.mock('../../hooks/useConversations', () => ({ useConversations: (h: string, s: string) => conversations(h, s) }))
const hostExecutions = vi.fn()
vi.mock('../../hooks/useHostExecutions', () => ({ useHostExecutions: (h: string) => hostExecutions(h) }))
import { WorkerGoneTab } from './WorkerGoneTab'
import { useI18nStore } from '../../stores/useI18nStore'
import type { ConversationRow, ConversationsPage } from '../../lib/nex/conversations-api'
import type { UseConversations } from '../../hooks/useConversations'

const HOME = '/Users/wake'
const HOUR = 3_600_000
const row = (o: Partial<ConversationRow>): ConversationRow => ({
  session_id: 'aaaaaaaa-0000-0000-0000-000000000000',
  title: 'untitled',
  title_source: 'ai',
  cwd: '/Users/wake/proj',
  cwd_exists: true,
  last_activity_at: Date.now() - 3 * HOUR - 10 * 60_000,
  last_in: 'terminal',
  ...o,
})
const pageOf = (rows: ConversationRow[], extra: Partial<ConversationsPage> = {}): ConversationsPage => ({
  state: 'gone', scanned_at: 1, home: HOME, total: rows.length, truncated: false, unknown_owner: 0, conversations: rows, ...extra,
})
const hook = (o: Partial<UseConversations> = {}): UseConversations => ({
  page: null, phase: 'ready', error: null, unavailable: false, refetch: vi.fn(), ...o,
})
const ready = (rows: ConversationRow[], extra: Partial<ConversationsPage> = {}, o: Partial<UseConversations> = {}) =>
  hook({ page: pageOf(rows, extra), ...o })

const TERM = row({
  session_id: '11111111-aaaa-bbbb-cccc-000000000001', title: 'Terminal chat', cwd: '/Users/wake/Workspace/purdex',
  first_prompt: 'refactor the Session picker', last_in: 'terminal',
})
const WORKER = row({
  session_id: '22222222-aaaa-bbbb-cccc-000000000002', title: 'Worker chat', cwd: '/srv/jobs/nightly',
  first_prompt: 'run the nightly report', last_in: 'worker', latest_execution_id: 'exec-9',
})

const rowByTitle = (title: string) => screen.getAllByTestId('conversation-row').find((r) => r.textContent?.includes(title))!

describe('WorkerGoneTab', () => {
  beforeEach(() => {
    conversations.mockReset()
    hostExecutions.mockReset()
    act(() => { useI18nStore.getState().setLocale('zh-TW') })
  })
  afterEach(() => { act(() => { useI18nStore.getState().setLocale('en') }) })

  it('reads the gone conversations of its host, and holds no live-execution subscription (R-4-12)', () => {
    conversations.mockReturnValue(ready([]))
    render(<WorkerGoneTab hostId="h1" />)
    expect(conversations).toHaveBeenCalledWith('h1', 'gone')
    expect(hostExecutions).not.toHaveBeenCalled()
  })

  it('every row is disabled (U3): aria-disabled, no button, the note; title, ~ cwd and age still shown', () => {
    conversations.mockReturnValue(ready([TERM, WORKER]))
    render(<WorkerGoneTab hostId="h1" />)
    const rows = screen.getAllByTestId('conversation-row')
    expect(rows).toHaveLength(2)
    for (const r of rows) {
      expect(r).toHaveAttribute('data-state', 'gone')
      expect(r).toHaveAttribute('aria-disabled', 'true')
      expect(within(r).queryByRole('button')).toBeNull()
      expect(within(r).getByTestId('conversation-row-gone-note')).toHaveTextContent('對話檔已清除，無法再啟動')
    }
    expect(screen.queryByTestId('conversation-row-rebuild')).toBeNull()
    const term = rowByTitle('Terminal chat')
    expect(within(term).getByTestId('conversation-row-cwd')).toHaveTextContent('~/Workspace/purdex')
    expect(within(term).getByTestId('conversation-row-age')).toHaveTextContent('3 小時前')
    expect(within(rowByTitle('Worker chat')).getByTestId('conversation-row-cwd')).toHaveTextContent('/srv/jobs/nightly')
  })

  describe('search', () => {
    const search = (q: string) => fireEvent.change(screen.getByTestId('worker-gone-search'), { target: { value: q } })
    const titles = () => screen.queryAllByTestId('conversation-row').map((r) => r.textContent)
    beforeEach(() => {
      conversations.mockReturnValue(ready([TERM, WORKER]))
      render(<WorkerGoneTab hostId="h1" />)
    })

    it('has its own placeholder', () => {
      expect(screen.getByTestId('worker-gone-search')).toHaveAttribute('placeholder', '搜尋已消失的對話')
    })
    it('by title, case-insensitively', () => {
      search('WORKER CHAT')
      expect(titles()).toEqual([expect.stringContaining('Worker chat')])
    })
    it('by the displayed ~/ cwd', () => {
      search('~/workspace')
      expect(titles()).toEqual([expect.stringContaining('Terminal chat')])
    })
    it('by the raw cwd', () => {
      search('/USERS/WAKE/WORKSPACE')
      expect(titles()).toEqual([expect.stringContaining('Terminal chat')])
    })
    it('by the first prompt', () => {
      search('Nightly REPORT')
      expect(titles()).toEqual([expect.stringContaining('Worker chat')])
    })
    it('by the session id', () => {
      search('22222222-AAAA')
      expect(titles()).toEqual([expect.stringContaining('Worker chat')])
    })
    it('nothing matches → the empty line', () => {
      search('zzz-nothing')
      expect(titles()).toEqual([])
      expect(screen.getByTestId('worker-gone-empty')).toHaveTextContent('沒有已消失的對話')
    })
  })

  it('a root error shows its notice and no rows, not even an empty line (R-4-1)', () => {
    conversations.mockReturnValue(ready([TERM, WORKER], { root_error: 'lstat /Volumes/PD1KAVault: no such file or directory' }))
    render(<WorkerGoneTab hostId="h1" />)
    expect(screen.getByTestId('worker-gone-root-error'))
      .toHaveTextContent('無法讀取對話檔目錄：lstat /Volumes/PD1KAVault: no such file or directory')
    expect(screen.queryAllByTestId('conversation-row')).toHaveLength(0)
    expect(screen.queryByRole('list')).toBeNull()
    expect(screen.queryByTestId('worker-gone-empty')).toBeNull()
  })

  it('no root error → no notice', () => {
    conversations.mockReturnValue(ready([TERM]))
    render(<WorkerGoneTab hostId="h1" />)
    expect(screen.queryByTestId('worker-gone-root-error')).toBeNull()
    expect(screen.getAllByTestId('conversation-row')).toHaveLength(1)
  })

  it('the cap notice when the page is truncated', () => {
    conversations.mockReturnValue(ready([TERM], { truncated: true, total: 2400 }))
    render(<WorkerGoneTab hostId="h1" />)
    expect(screen.getByTestId('worker-gone-truncated')).toHaveTextContent('超過 2,000 筆，較舊的沒有列出')
  })

  it('never shows the unknown-owner line (R-4-15)', () => {
    conversations.mockReturnValue(ready([TERM], { unknown_owner: 3 }))
    const { rerender } = render(<WorkerGoneTab hostId="h1" />)
    expect(screen.queryByText(/另有 3 個對話/)).toBeNull()
    expect(screen.queryByTestId('worker-exited-unknown-owner')).toBeNull()
    conversations.mockReturnValue(ready([], { unknown_owner: 2 }))
    rerender(<WorkerGoneTab hostId="h1" />)
    expect(screen.getByTestId('worker-gone-empty')).toBeInTheDocument()
    expect(screen.queryByText(/另有 2 個對話/)).toBeNull()
  })

  it('Nexen disabled on the host → the unavailable notice, no error line', () => {
    conversations.mockReturnValue(hook({ phase: 'error', error: 'http_404', unavailable: true }))
    render(<WorkerGoneTab hostId="h1" />)
    expect(screen.getByTestId('worker-gone-unavailable')).toHaveTextContent('這台主機沒有啟用 Nexen')
    expect(screen.queryByTestId('worker-gone-error')).toBeNull()
    expect(screen.queryByTestId('worker-gone-empty')).toBeNull()
  })

  it('an error shows the error line; retry refetches', () => {
    const refetch = vi.fn()
    conversations.mockReturnValue(hook({ phase: 'error', error: 'conversations_unavailable', refetch }))
    render(<WorkerGoneTab hostId="h1" />)
    expect(screen.getByTestId('worker-gone-error')).toHaveTextContent('conversations_unavailable')
    expect(screen.queryByTestId('worker-gone-empty')).toBeNull()
    expect(screen.queryByTestId('worker-gone-unavailable')).toBeNull()
    fireEvent.click(screen.getByTestId('worker-gone-retry'))
    expect(refetch).toHaveBeenCalledTimes(1)
  })

  it('the first load shows the loading line and no empty line', () => {
    conversations.mockReturnValue(hook({ phase: 'loading' }))
    render(<WorkerGoneTab hostId="h1" />)
    expect(screen.getByTestId('worker-gone-loading')).toHaveTextContent('載入中…')
    expect(screen.queryByTestId('worker-gone-empty')).toBeNull()
  })

  it('a retry with rows kept shows the loading line and aria-busy over the rows (#1630)', () => {
    conversations.mockReturnValue(ready([TERM], {}, { phase: 'loading' }))
    const { rerender } = render(<WorkerGoneTab hostId="h1" />)
    expect(screen.getByTestId('worker-gone-loading')).toBeInTheDocument()
    expect(screen.getByRole('list')).toHaveAttribute('aria-busy', 'true')
    expect(screen.getAllByTestId('conversation-row')).toHaveLength(1)
    conversations.mockReturnValue(ready([TERM]))
    rerender(<WorkerGoneTab hostId="h1" />)
    expect(screen.queryByTestId('worker-gone-loading')).toBeNull()
    expect(screen.getByRole('list')).not.toHaveAttribute('aria-busy')
  })

  it('an empty ready page shows the empty line', () => {
    conversations.mockReturnValue(ready([]))
    render(<WorkerGoneTab hostId="h1" />)
    expect(screen.getByTestId('worker-gone-empty')).toHaveTextContent('沒有已消失的對話')
    expect(screen.queryByTestId('worker-gone-truncated')).toBeNull()
  })

  it('renders nothing without a host', () => {
    conversations.mockReturnValue(ready([]))
    const { container } = render(<WorkerGoneTab />)
    expect(container).toBeEmptyDOMElement()
    expect(conversations).not.toHaveBeenCalled()
  })
})
