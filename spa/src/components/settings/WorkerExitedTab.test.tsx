import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, within, act } from '@testing-library/react'

const conversations = vi.fn()
vi.mock('../../hooks/useConversations', () => ({ useConversations: (h: string, s: string) => conversations(h, s) }))
const hostExecutions = vi.fn()
vi.mock('../../hooks/useHostExecutions', () => ({ useHostExecutions: (h: string) => hostExecutions(h) }))
const openWorkerTab = vi.fn()
vi.mock('../../features/workspace/lib/open-worker-tab', () => ({ openWorkerTab: (c: unknown) => openWorkerTab(c) }))
const openConversationRebuild = vi.fn()
vi.mock('../../lib/nex/open-conversation-rebuild', () => ({
  openConversationRebuild: (h: string, r: unknown) => openConversationRebuild(h, r),
}))
import { WorkerExitedTab } from './WorkerExitedTab'
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
  state: 'ended', scanned_at: 1, home: HOME, total: rows.length, truncated: false, unknown_owner: 0, conversations: rows, ...extra,
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
const WORKER_NO_STINT = row({
  session_id: '33333333-aaaa-bbbb-cccc-000000000003', title: 'Sdk resumed', cwd: '/Users/wake/x', last_in: 'worker',
})

const rowByTitle = (title: string) => screen.getAllByTestId('conversation-row').find((r) => r.textContent?.includes(title))!

describe('WorkerExitedTab', () => {
  beforeEach(() => {
    conversations.mockReset()
    hostExecutions.mockReset()
    openWorkerTab.mockReset()
    openConversationRebuild.mockReset()
    openConversationRebuild.mockResolvedValue('t1')
    act(() => { useI18nStore.getState().setLocale('zh-TW') })
  })
  afterEach(() => { act(() => { useI18nStore.getState().setLocale('en') }) })

  it('reads the ended conversations of its host, and holds no live-execution subscription (R-4-12)', () => {
    conversations.mockReturnValue(ready([]))
    render(<WorkerExitedTab hostId="h1" />)
    expect(conversations).toHaveBeenCalledWith('h1', 'ended')
    expect(hostExecutions).not.toHaveBeenCalled()
  })

  it('lists terminal-last and worker-last rows with 上次在, the ~ cwd and the age', () => {
    conversations.mockReturnValue(ready([TERM, WORKER]))
    render(<WorkerExitedTab hostId="h1" />)
    expect(screen.getAllByTestId('conversation-row')).toHaveLength(2)
    const term = rowByTitle('Terminal chat')
    expect(within(term).getByTestId('conversation-row-last-in')).toHaveTextContent('上次在終端機')
    expect(within(term).getByTestId('conversation-row-cwd')).toHaveTextContent('~/Workspace/purdex')
    expect(within(term).getByTestId('conversation-row-age')).toHaveTextContent('3 小時前')
    const worker = rowByTitle('Worker chat')
    expect(within(worker).getByTestId('conversation-row-last-in')).toHaveTextContent('上次在 Worker')
    expect(within(worker).getByTestId('conversation-row-cwd')).toHaveTextContent('/srv/jobs/nightly')
  })

  describe('search', () => {
    const search = (q: string) => fireEvent.change(screen.getByTestId('worker-exited-search'), { target: { value: q } })
    const titles = () => screen.queryAllByTestId('conversation-row').map((r) => r.textContent)
    beforeEach(() => {
      conversations.mockReturnValue(ready([TERM, WORKER]))
      render(<WorkerExitedTab hostId="h1" />)
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
      expect(screen.getByTestId('worker-exited-empty')).toBeInTheDocument()
    })
  })

  describe('rebuild routing', () => {
    it('a worker-last row with a stint opens that execution', () => {
      conversations.mockReturnValue(ready([WORKER]))
      render(<WorkerExitedTab hostId="h1" />)
      fireEvent.click(screen.getByTestId('conversation-row-rebuild'))
      expect(openWorkerTab).toHaveBeenCalledWith({ kind: 'execution', executionId: 'exec-9', host: 'h1' })
      expect(openConversationRebuild).not.toHaveBeenCalled()
    })

    it('a terminal-last row opens the conversation rebuild tab, even when it has a stint', () => {
      const t = { ...TERM, latest_execution_id: 'exec-old' }
      conversations.mockReturnValue(ready([t]))
      render(<WorkerExitedTab hostId="h1" />)
      fireEvent.click(screen.getByTestId('conversation-row-rebuild'))
      expect(openConversationRebuild).toHaveBeenCalledWith('h1', t)
      expect(openWorkerTab).not.toHaveBeenCalled()
    })

    it('a worker-last row without a stint opens the conversation rebuild tab (R-4-5)', () => {
      conversations.mockReturnValue(ready([WORKER_NO_STINT]))
      render(<WorkerExitedTab hostId="h1" />)
      fireEvent.click(screen.getByTestId('conversation-row-rebuild'))
      expect(openConversationRebuild).toHaveBeenCalledWith('h1', WORKER_NO_STINT)
      expect(openWorkerTab).not.toHaveBeenCalled()
    })
  })

  it('rows with no cwd or a missing cwd have no rebuild button', () => {
    conversations.mockReturnValue(ready([
      row({ session_id: 's-nocwd', title: 'No cwd', cwd: undefined, cwd_exists: false }),
      row({ session_id: 's-gone', title: 'Cwd gone', cwd: '/Users/wake/old', cwd_exists: false }),
    ]))
    render(<WorkerExitedTab hostId="h1" />)
    expect(screen.queryByTestId('conversation-row-rebuild')).toBeNull()
    expect(within(rowByTitle('Cwd gone')).getByTestId('conversation-row-cwd-missing')).toHaveTextContent('工作目錄已不存在')
    expect(within(rowByTitle('No cwd')).queryByTestId('conversation-row-cwd-missing')).toBeNull()
  })

  it('a root error shows its notice, keeps the rows and disables every rebuild (R-4-1)', () => {
    conversations.mockReturnValue(ready([TERM, WORKER], { root_error: 'lstat /Volumes/PD1KAVault: no such file or directory' }))
    render(<WorkerExitedTab hostId="h1" />)
    expect(screen.getByTestId('worker-exited-root-error'))
      .toHaveTextContent('無法讀取對話檔目錄：lstat /Volumes/PD1KAVault: no such file or directory')
    expect(screen.getAllByTestId('conversation-row')).toHaveLength(2)
    const buttons = screen.getAllByTestId('conversation-row-rebuild')
    expect(buttons).toHaveLength(2)
    for (const b of buttons) {
      expect(b).toBeDisabled()
      fireEvent.click(b)
    }
    expect(openWorkerTab).not.toHaveBeenCalled()
    expect(openConversationRebuild).not.toHaveBeenCalled()
  })

  it('no root error → no notice and enabled buttons', () => {
    conversations.mockReturnValue(ready([TERM]))
    render(<WorkerExitedTab hostId="h1" />)
    expect(screen.queryByTestId('worker-exited-root-error')).toBeNull()
    expect(screen.getByTestId('conversation-row-rebuild')).not.toBeDisabled()
  })

  it('the cap notice when the page is truncated', () => {
    conversations.mockReturnValue(ready([TERM], { truncated: true, total: 2400 }))
    render(<WorkerExitedTab hostId="h1" />)
    expect(screen.getByTestId('worker-exited-truncated')).toHaveTextContent('超過 2,000 筆，較舊的沒有列出')
  })

  it('unknown owners → a muted line after the list (R-4-15); none → no line', () => {
    conversations.mockReturnValue(ready([TERM], { unknown_owner: 3 }))
    const { rerender } = render(<WorkerExitedTab hostId="h1" />)
    const line = screen.getByTestId('worker-exited-unknown-owner')
    expect(line).toHaveTextContent('另有 3 個對話的狀態無法確認，未列出')
    expect(screen.getByRole('list').compareDocumentPosition(line) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    conversations.mockReturnValue(ready([TERM], { unknown_owner: 0 }))
    rerender(<WorkerExitedTab hostId="h1" />)
    expect(screen.queryByTestId('worker-exited-unknown-owner')).toBeNull()
  })

  it('unknown owners are counted even when no row is listed', () => {
    conversations.mockReturnValue(ready([], { unknown_owner: 1 }))
    render(<WorkerExitedTab hostId="h1" />)
    expect(screen.getByTestId('worker-exited-empty')).toBeInTheDocument()
    expect(screen.getByTestId('worker-exited-unknown-owner')).toHaveTextContent('另有 1 個對話的狀態無法確認，未列出')
  })

  it('Nexen disabled on the host → the unavailable notice, no error line', () => {
    conversations.mockReturnValue(hook({ phase: 'error', error: 'http_404', unavailable: true }))
    render(<WorkerExitedTab hostId="h1" />)
    expect(screen.getByTestId('worker-exited-unavailable')).toHaveTextContent('這台主機沒有啟用 Nexen')
    expect(screen.queryByTestId('worker-exited-error')).toBeNull()
    expect(screen.queryByTestId('worker-exited-empty')).toBeNull()
  })

  it('an error shows the error line; retry refetches', () => {
    const refetch = vi.fn()
    conversations.mockReturnValue(hook({ phase: 'error', error: 'conversations_unavailable', refetch }))
    render(<WorkerExitedTab hostId="h1" />)
    expect(screen.getByTestId('worker-exited-error')).toHaveTextContent('conversations_unavailable')
    expect(screen.queryByTestId('worker-exited-empty')).toBeNull()
    expect(screen.queryByTestId('worker-exited-unavailable')).toBeNull()
    fireEvent.click(screen.getByTestId('worker-exited-retry'))
    expect(refetch).toHaveBeenCalledTimes(1)
  })

  it('the first load shows the loading line and no empty line', () => {
    conversations.mockReturnValue(hook({ phase: 'loading' }))
    render(<WorkerExitedTab hostId="h1" />)
    expect(screen.getByTestId('worker-exited-loading')).toBeInTheDocument()
    expect(screen.queryByTestId('worker-exited-empty')).toBeNull()
  })

  it('a retry with rows kept shows the loading line and aria-busy over the rows (#1630)', () => {
    conversations.mockReturnValue(ready([TERM], {}, { phase: 'loading' }))
    const { rerender } = render(<WorkerExitedTab hostId="h1" />)
    expect(screen.getByTestId('worker-exited-loading')).toBeInTheDocument()
    expect(screen.getByRole('list')).toHaveAttribute('aria-busy', 'true')
    expect(screen.getAllByTestId('conversation-row')).toHaveLength(1)
    conversations.mockReturnValue(ready([TERM]))
    rerender(<WorkerExitedTab hostId="h1" />)
    expect(screen.queryByTestId('worker-exited-loading')).toBeNull()
    expect(screen.getByRole('list')).not.toHaveAttribute('aria-busy')
  })

  it('an empty ready page shows the empty line', () => {
    conversations.mockReturnValue(ready([]))
    render(<WorkerExitedTab hostId="h1" />)
    expect(screen.getByTestId('worker-exited-empty')).toHaveTextContent('沒有已退出的對話')
    expect(screen.queryByTestId('worker-exited-truncated')).toBeNull()
    expect(screen.queryByTestId('worker-exited-unknown-owner')).toBeNull()
  })

  it('renders nothing without a host', () => {
    conversations.mockReturnValue(ready([]))
    const { container } = render(<WorkerExitedTab />)
    expect(container).toBeEmptyDOMElement()
    expect(conversations).not.toHaveBeenCalled()
  })
})
