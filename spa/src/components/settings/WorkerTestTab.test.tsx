import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, within } from '@testing-library/react'

const conversations = vi.fn()
vi.mock('../../hooks/useConversations', () => ({ useConversations: (h: string, s: string, sc?: string) => conversations(h, s, sc) }))
const listExecutions = vi.fn()
vi.mock('../../lib/nex/nex-api', () => ({ listExecutions: (...a: unknown[]) => listExecutions(...a), attachControl: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), archiveExecution: vi.fn() }))
vi.mock('../../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))
const openWorkerTab = vi.fn()
vi.mock('../../features/workspace/lib/open-worker-tab', () => ({ openWorkerTab: (c: unknown) => openWorkerTab(c) }))
const openConversationRebuild = vi.fn()
vi.mock('../../lib/nex/open-conversation-rebuild', () => ({ openConversationRebuild: (h: string, r: unknown) => openConversationRebuild(h, r) }))
import { WorkerTestTab } from './WorkerTestTab'
import { useI18nStore } from '../../stores/useI18nStore'
import { resetExecutionListForTests, useExecutionListStore } from '../../stores/useExecutionListStore'
import { useNexHostStore, type NexHostEntry } from '../../stores/useNexHostStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { subscriptionSlots } from '../../lib/nex/subscription-slots'
import type { ConversationRow, ConversationsPage } from '../../lib/nex/conversations-api'
import type { UseConversations } from '../../hooks/useConversations'
import type { ExecutionSummary } from '../../lib/nex/types'

const H = 'h1'
const HOME = '/Users/wake'
const crow = (o: Partial<ConversationRow>): ConversationRow => ({
  session_id: 'aaaaaaaa-0000-0000-0000-000000000000', title: 'untitled', title_source: 'ai', cwd: '/tmp/x', cwd_exists: true,
  last_activity_at: Date.now() - 3_600_000, last_in: 'terminal', ...o,
})
const page = (state: 'ended' | 'gone', rows: ConversationRow[], extra: Partial<ConversationsPage> = {}): ConversationsPage => ({
  state, scanned_at: 1, home: HOME, total: rows.length, truncated: false, unknown_owner: 0, conversations: rows, ...extra,
})
const hook = (o: Partial<UseConversations> = {}): UseConversations => ({
  page: null, phase: 'ready', error: null, unavailable: false, refetch: vi.fn(), ...o,
})
const erow = (o: Partial<ExecutionSummary> & { id: string }): ExecutionSummary =>
  ({ state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/tmp/live', mount_kind: 'dev', brief: 'live brief', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false, ...o }) as ExecutionSummary

const entry = (daemonCapabilities?: string[], over: Partial<NexHostEntry> = {}): NexHostEntry => ({
  info: { configured: true, mounted: true, ready: true, init_error: '', effective: null },
  capabilities: null, phase: 'ready', error: null, fetchedAt: 1, generation: 1, fingerprint: '1:1:t', daemonCapabilities, ...over,
})
const SCOPED = ['conversations.scope.v1']
const setHost = (e: NexHostEntry) => useNexHostStore.setState({ byHost: { [H]: e }, ensure: vi.fn().mockResolvedValue(undefined) })
const seedLive = (items: ExecutionSummary[]) =>
  useExecutionListStore.setState({ byHost: { [H]: { items, phase: 'ready', error: null, lastSeq: null, refreshRevision: 0, truncated: false } } })
/** ended / gone answers by state. */
const answer = (ended: UseConversations, gone: UseConversations) =>
  conversations.mockImplementation((_h: string, s: string) => (s === 'ended' ? ended : gone))

const ENDED = crow({ session_id: 'e1', title: 'Ended chat', cwd: '/tmp/ended-proj', first_prompt: 'banana split' })
const GONE = crow({ session_id: 'g1', title: 'Gone chat', cwd: '/tmp/gone-proj' })

describe('WorkerTestTab', () => {
  beforeEach(() => {
    conversations.mockReset()
    listExecutions.mockReset().mockResolvedValue({ items: [], next_cursor: '' })
    openWorkerTab.mockReset()
    openConversationRebuild.mockReset().mockResolvedValue('t1')
    subscriptionSlots.resetForTests()
    resetExecutionListForTests()
    useShownHostsStore.setState({ ids: [H] })
    setHost(entry(SCOPED))
    seedLive([])
    act(() => { useI18nStore.getState().setLocale('zh-TW') })
  })
  afterEach(() => { act(() => { useI18nStore.getState().setLocale('en') }) })

  it('renders nothing without a host', () => {
    const { container } = render(<WorkerTestTab />)
    expect(container).toBeEmptyDOMElement()
  })

  it('reads the ended and gone lists with scope=test', () => {
    answer(hook({ page: page('ended', []) }), hook({ page: page('gone', []) }))
    render(<WorkerTestTab hostId={H} />)
    expect(conversations).toHaveBeenCalledWith(H, 'ended', 'test')
    expect(conversations).toHaveBeenCalledWith(H, 'gone', 'test')
  })

  it('shows the three sections, each with its own rows', () => {
    seedLive([erow({ id: 'L1', session_id: 'SL', cwd: '/tmp/live', brief: 'live brief' }), erow({ id: 'N1', session_id: 'SN', cwd: '/Users/wake/p', brief: 'normal brief' })])
    answer(hook({ page: page('ended', [ENDED]) }), hook({ page: page('gone', [GONE]) }))
    render(<WorkerTestTab hostId={H} />)
    expect(screen.getByTestId('worker-test-section-live')).toHaveTextContent('執行中')
    expect(screen.getByTestId('worker-test-section-exited')).toHaveTextContent('已退出')
    expect(screen.getByTestId('worker-test-section-gone')).toHaveTextContent('已消失')
    expect(screen.getByText('live brief')).toBeInTheDocument()
    expect(screen.queryByText('normal brief')).toBeNull()
    expect(screen.getByText('Ended chat')).toBeInTheDocument()
    expect(screen.getByText('Gone chat')).toBeInTheDocument()
    expect(screen.queryByTestId('worker-test-empty')).toBeNull()
  })

  it('shows the test scope\'s unknown-owner count (the normal tabs cannot see it, codex R1)', () => {
    answer(hook({ page: page('ended', [], { unknown_owner: 3 }) }), hook({ page: page('gone', []) }))
    render(<WorkerTestTab hostId={H} />)
    expect(screen.getByTestId('worker-test-unknown-owner')).toHaveTextContent('3')
  })

  it('an empty section is not shown', () => {
    answer(hook({ page: page('ended', [ENDED]) }), hook({ page: page('gone', []) }))
    render(<WorkerTestTab hostId={H} />)
    expect(screen.getByTestId('worker-test-section-exited')).toBeInTheDocument()
    expect(screen.queryByTestId('worker-test-section-live')).toBeNull()
    expect(screen.queryByTestId('worker-test-section-gone')).toBeNull()
    expect(screen.queryByTestId('worker-test-empty')).toBeNull()
  })

  it('all three empty and loaded shows the empty copy', () => {
    answer(hook({ page: page('ended', []) }), hook({ page: page('gone', []) }))
    render(<WorkerTestTab hostId={H} />)
    expect(screen.getByTestId('worker-test-empty')).toBeInTheDocument()
  })

  it('does not show the empty copy while a list is still loading', () => {
    answer(hook({ page: null, phase: 'loading' }), hook({ page: page('gone', []) }))
    render(<WorkerTestTab hostId={H} />)
    expect(screen.queryByTestId('worker-test-empty')).toBeNull()
    expect(screen.getByTestId('worker-test-exited-loading')).toBeInTheDocument()
  })

  it('the live search finds a handoff row by its shown title only with the host capability (#1771)', () => {
    seedLive([erow({ id: 'L1', session_id: 'SL', cwd: '/tmp/repo', brief: '', session_title: { text: 'Zebrafinch', source: 'ai' } })])
    answer(hook({ page: page('ended', []) }), hook({ page: page('gone', []) }))
    setHost(entry(SCOPED, { capabilities: { session_title: { sources: ['ai'], max_bytes: 200 } } as never }))
    const { unmount } = render(<WorkerTestTab hostId={H} />)
    fireEvent.change(screen.getByTestId('worker-test-search'), { target: { value: 'zebrafinch' } })
    expect(screen.getAllByTestId('executions-row')).toHaveLength(1)
    expect(screen.queryByTestId('worker-test-empty')).toBeNull()
    unmount()
    setHost(entry(SCOPED))
    render(<WorkerTestTab hostId={H} />)
    fireEvent.change(screen.getByTestId('worker-test-search'), { target: { value: 'zebrafinch' } })
    expect(screen.queryByTestId('executions-row')).toBeNull()
  })

  it('one keyword filters all three sections', () => {
    seedLive([erow({ id: 'L1', session_id: 'SL', cwd: '/tmp/banana-live', brief: 'live brief' }), erow({ id: 'L2', session_id: 'SL2', cwd: '/tmp/other', brief: 'other live' })])
    answer(
      hook({ page: page('ended', [ENDED, crow({ session_id: 'e2', title: 'Plain', cwd: '/tmp/plain', first_prompt: 'x' })]) }),
      hook({ page: page('gone', [GONE, crow({ session_id: 'g2', title: 'Banana gone', cwd: '/tmp/b' })]) }),
    )
    render(<WorkerTestTab hostId={H} />)
    fireEvent.change(screen.getByTestId('worker-test-search'), { target: { value: 'banana' } })
    expect(screen.getByText('live brief')).toBeInTheDocument()
    expect(screen.queryByText('other live')).toBeNull()
    expect(screen.getByText('Ended chat')).toBeInTheDocument()
    expect(screen.queryByText('Plain')).toBeNull()
    expect(screen.getByText('Banana gone')).toBeInTheDocument()
    expect(screen.queryByText('Gone chat')).toBeNull()
  })

  it('the keyword matching nothing hides every section and shows the empty copy', () => {
    answer(hook({ page: page('ended', [ENDED]) }), hook({ page: page('gone', [GONE]) }))
    render(<WorkerTestTab hostId={H} />)
    fireEvent.change(screen.getByTestId('worker-test-search'), { target: { value: 'zzzz' } })
    expect(screen.getByTestId('worker-test-empty')).toBeInTheDocument()
  })

  it('exited rows rebuild as the Exited tab does; gone rows are disabled', () => {
    const w = crow({ session_id: 'e9', title: 'Worker chat', last_in: 'worker', latest_execution_id: 'exec-9' })
    answer(hook({ page: page('ended', [w]) }), hook({ page: page('gone', [GONE]) }))
    render(<WorkerTestTab hostId={H} />)
    const rows = screen.getAllByTestId('conversation-row')
    fireEvent.click(within(rows[0]).getByTestId('conversation-row-rebuild'))
    expect(openWorkerTab).toHaveBeenCalledWith({ kind: 'execution', executionId: 'exec-9', host: H })
    expect(rows[1]).toHaveAttribute('aria-disabled', 'true')
  })

  it('a root error disables rebuild and hides the gone rows', () => {
    answer(hook({ page: page('ended', [ENDED], { root_error: 'EACCES' }) }), hook({ page: page('gone', [GONE], { root_error: 'EACCES' }) }))
    render(<WorkerTestTab hostId={H} />)
    expect(screen.getByTestId('conversation-row-rebuild')).toBeDisabled()
    expect(screen.queryByText('Gone chat')).toBeNull()
    expect(screen.getAllByText(/EACCES/).length).toBeGreaterThan(0)
  })

  it('shows per-section error and retry', () => {
    const refetch = vi.fn()
    answer(hook({ page: null, phase: 'error', error: 'boom', refetch }), hook({ page: page('gone', []) }))
    render(<WorkerTestTab hostId={H} />)
    expect(screen.getByTestId('worker-test-exited-error')).toHaveTextContent('boom')
    fireEvent.click(screen.getByTestId('worker-test-exited-retry'))
    expect(refetch).toHaveBeenCalled()
  })

  it('shows truncation per section', () => {
    answer(hook({ page: page('ended', [ENDED], { truncated: true }) }), hook({ page: page('gone', [GONE], { truncated: true }) }))
    render(<WorkerTestTab hostId={H} />)
    expect(screen.getByTestId('worker-test-exited-truncated')).toBeInTheDocument()
    expect(screen.getByTestId('worker-test-gone-truncated')).toBeInTheDocument()
  })

  describe('a host whose daemon has no conversations.scope.v1', () => {
    it('shows only the explanation and calls no API', () => {
      setHost(entry([]))
      seedLive([erow({ id: 'L1', session_id: 'SL' })])
      render(<WorkerTestTab hostId={H} />)
      expect(screen.getByTestId('worker-test-unsupported')).toBeInTheDocument()
      expect(screen.queryByTestId('worker-test-search')).toBeNull()
      expect(screen.queryByTestId('executions-row')).toBeNull()
      expect(conversations).not.toHaveBeenCalled()
      expect(listExecutions).not.toHaveBeenCalled()
    })
    it('a host still loading shows no explanation yet', () => {
      setHost(entry(undefined, { phase: 'loading', info: null }))
      render(<WorkerTestTab hostId={H} />)
      expect(screen.queryByTestId('worker-test-unsupported')).toBeNull()
      expect(conversations).not.toHaveBeenCalled()
    })
  })
})
