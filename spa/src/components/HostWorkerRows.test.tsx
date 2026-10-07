// spa/src/components/HostWorkerRows.test.tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { listExecutions } from '../lib/nex/nex-api'
import { HostWorkerRows } from './HostWorkerRows'
import { resetExecutionListForTests, useExecutionListStore } from '../stores/useExecutionListStore'
import { useNexHostStore, type NexHostEntry } from '../stores/useNexHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { subscriptionSlots } from '../lib/nex/subscription-slots'
import { exitWorker } from '../lib/nex/exit-worker'
import { openNexSse, type NexSseOptions } from '../lib/nex/nex-sse'
import type { ExecutionSummary } from '../lib/nex/types'

vi.mock('../lib/nex/nex-api', () => ({ listExecutions: vi.fn().mockResolvedValue({ items: [], next_cursor: '' }), attachControl: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), archiveExecution: vi.fn() }))
vi.mock('../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))
vi.mock('../lib/nex/exit-worker', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/nex/exit-worker')>()),
  exitWorker: vi.fn(),
}))

const H = 'host-a'
const P = 'pfx'
const row = (over: Partial<ExecutionSummary> & { id: string }): ExecutionSummary =>
  ({ state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'brief', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false, ...over }) as ExecutionSummary

const readyEntry: NexHostEntry = {
  info: { configured: true, mounted: true, ready: true, init_error: '', effective: null },
  capabilities: null, phase: 'ready', error: null, fetchedAt: 1, generation: 1, fingerprint: '1:1:t',
}

function seed(items: ExecutionSummary[], patch: Record<string, unknown> = {}) {
  useExecutionListStore.setState({ byHost: { [H]: { items, phase: 'ready', error: null, lastSeq: null, refreshRevision: 0, truncated: false, ...patch } } })
}

beforeEach(() => {
  subscriptionSlots.resetForTests()
  resetExecutionListForTests()
  useExecutionListStore.setState({ byHost: {} })
  useNexHostStore.setState({ byHost: { [H]: readyEntry }, ensure: vi.fn().mockResolvedValue(undefined) })
  useShownHostsStore.setState({ ids: [H] })
  vi.mocked(exitWorker).mockReset().mockResolvedValue({ exited: true, terminated: true, archived: true, state: 'terminated' })
})

const renderRows = (onOpen = vi.fn(), filter?: 'normal' | 'test') =>
  render(<HostWorkerRows hostId={H} onOpen={onOpen} testIdPrefix={P} filter={filter} />)

describe('HostWorkerRows', () => {
  it('shows live rows only, one per conversation, and opens one', () => {
    seed([
      row({ id: 'E1', session_id: 'S' }),
      row({ id: 'E2', state: 'terminated', session_id: 'T' }),
      row({ id: 'E0', session_id: 'S', created_at: -1 }),
    ])
    const onOpen = vi.fn()
    renderRows(onOpen)
    const rows = screen.getAllByTestId('executions-row')
    expect(rows).toHaveLength(1)
    fireEvent.click(rows[0])
    expect(onOpen).toHaveBeenCalledWith('E1')
  })

  it('a disabled host shows the disabled copy, not loading', () => {
    useNexHostStore.setState({ byHost: { [H]: { ...readyEntry, phase: 'disabled' } } })
    renderRows()
    expect(screen.getByTestId(`${P}-disabled`)).toBeInTheDocument()
    expect(screen.queryByTestId(`${P}-loading`)).toBeNull()
  })

  it('an unavailable host shows its error, not loading', () => {
    useNexHostStore.setState({ byHost: { [H]: { ...readyEntry, phase: 'unavailable', error: 'down hard' } } })
    renderRows()
    expect(screen.getByTestId(`${P}-unavailable`)).toHaveTextContent('down hard')
    expect(screen.queryByTestId(`${P}-loading`)).toBeNull()
  })

  it('shows the empty state', () => {
    seed([row({ id: 'E2', state: 'terminated' })])
    renderRows()
    expect(screen.getByTestId(`${P}-empty`)).toBeInTheDocument()
  })

  it('shows an error with a retry that refetches', async () => {
    vi.mocked(listExecutions).mockRejectedValueOnce(new Error('boom'))
    renderRows()
    await act(async () => { await Promise.resolve() })
    await waitFor(() => expect(screen.getByTestId(`${P}-error`)).toHaveTextContent('boom'))
    expect(screen.queryByTestId(`${P}-empty`)).toBeNull()
    vi.mocked(listExecutions).mockResolvedValueOnce({ items: [row({ id: 'E9', brief: 'fresh' })], next_cursor: '' })
    fireEvent.click(screen.getByTestId(`${P}-retry`))
    await waitFor(() => expect(screen.getByText('fresh')).toBeInTheDocument())
    expect(screen.queryByTestId(`${P}-error`)).toBeNull()
  })

  it('shows a loading state before the first page', () => {
    seed([], { phase: 'loading' })
    renderRows()
    expect(screen.getByTestId(`${P}-loading`)).toBeInTheDocument()
  })

  it('loading with only non-live rows still shows the skeleton (keyed on the live row count)', () => {
    seed([row({ id: 'E1', state: 'terminated', session_id: 'T' }), row({ id: 'E2', archived: true, session_id: 'A' })], { phase: 'loading' })
    renderRows()
    expect(screen.getByTestId(`${P}-loading`)).toBeInTheDocument()
    expect(screen.queryByTestId('executions-row')).toBeNull()
  })

  it('loading with a live row already cached shows the row, not the skeleton', () => {
    seed([row({ id: 'E1', session_id: 'S' })], { phase: 'loading' })
    renderRows()
    expect(screen.queryByTestId(`${P}-loading`)).toBeNull()
    expect(screen.getAllByTestId('executions-row')).toHaveLength(1)
  })

  it('shows the truncation notice', () => {
    seed([row({ id: 'E1' })], { truncated: true })
    renderRows()
    expect(screen.getByTestId(`${P}-truncated`)).toBeInTheDocument()
  })

  it('a hidden host renders plain rows: no open handler, no exit', () => {
    useShownHostsStore.setState({ ids: [] })
    seed([row({ id: 'E1' })])
    const onOpen = vi.fn()
    renderRows(onOpen)
    const r = screen.getByTestId('executions-row')
    expect(r.tagName).not.toBe('BUTTON')
    fireEvent.click(r)
    expect(onOpen).not.toHaveBeenCalled()
    expect(screen.queryByTestId('executions-row-exit')).toBeNull()
  })

  // #1771 (New Tab / Settings Workers): the host id reaches the row, so the session_title capability gate is this host's.
  describe('a handoff row (empty brief) is named (#1771)', () => {
    const handoff = () => row({ id: 'E1', brief: '', cwd: '/w/repo', session_title: { text: 'Zebrafinch', source: 'ai' } })
    it('with the host\'s session_title capability: the conversation title', () => {
      useNexHostStore.setState({ byHost: { [H]: { ...readyEntry, capabilities: { session_title: { sources: ['ai'], max_bytes: 200 } } as never } } })
      seed([handoff()])
      renderRows()
      expect(screen.getByTestId('executions-brief').textContent).toBe('Zebrafinch')
    })
    it('without it: the cwd basename', () => {
      seed([handoff()])
      renderRows()
      expect(screen.getByTestId('executions-brief').textContent).toBe('repo')
    })
  })

  it('exits an idle row at once', async () => {
    seed([row({ id: 'E1', state: 'idle' })])
    renderRows()
    await act(async () => { fireEvent.click(screen.getByTestId('executions-row-exit')) })
    expect(exitWorker).toHaveBeenCalledWith({ hostId: H, executionId: 'E1' })
  })

  // Permission channel Review Focus #6 (consumer guide §9.8 §2): the site-wide stream carries relay fields only, so a
  // permission frame is the signal and the row comes from the list refetch — a row of a list that is not open in any
  // pane shows 「等待核准」 once its refetched summary carries pending_permission, and drops it when that turns null.
  it('a permission frame on the site-wide stream refetches the rows: the row gains, then loses, 等待核准', async () => {
    const site: { opts?: NexSseOptions } = {}
    vi.mocked(openNexSse).mockImplementation((o) => { site.opts = o; return { close: vi.fn() } })
    const pending = { request_id: 'r1', tool_name: 'Bash', since: 5 }
    vi.mocked(listExecutions)
      .mockResolvedValueOnce({ items: [row({ id: 'E1', state: 'running', brief: 'busy', pending_permission: null })], next_cursor: '' })
      .mockResolvedValueOnce({ items: [row({ id: 'E1', state: 'running', brief: 'busy', pending_permission: pending })], next_cursor: '' })
      .mockResolvedValueOnce({ items: [row({ id: 'E1', state: 'running', brief: 'busy', pending_permission: null })], next_cursor: '' })
    const callsBefore = vi.mocked(listExecutions).mock.calls.length
    renderRows()
    await waitFor(() => expect(screen.getByText('busy')).toBeInTheDocument())
    expect(screen.queryByTestId('executions-awaiting')).toBeNull()
    expect(site.opts?.url.split('?')[0]).toBe('/api/nex/v1/events')

    act(() => { site.opts!.onFrame({ id: '41', event: 'permission.requested', data: JSON.stringify({ execution_id: 'E1', request_id: 'r1', tool_name: 'Bash' }) }) })
    await waitFor(() => expect(screen.getByTestId('executions-awaiting')).toBeInTheDocument(), { timeout: 2000 })
    expect(screen.getByTestId('executions-state-dot')).toHaveClass('bg-status-warning')

    act(() => { site.opts!.onFrame({ id: '42', event: 'permission.resolved', data: JSON.stringify({ execution_id: 'E1', request_id: 'r1', outcome: 'allowed' }) }) })
    await waitFor(() => expect(screen.queryByTestId('executions-awaiting')).toBeNull(), { timeout: 2000 })
    expect(screen.getByTestId('executions-state-dot')).toHaveClass('bg-status-success')
    expect(vi.mocked(listExecutions).mock.calls.length - callsBefore).toBe(3)
  })

  it('asks before exiting a running row', async () => {
    seed([row({ id: 'E1', state: 'running' })])
    renderRows()
    fireEvent.click(screen.getByTestId('executions-row-exit'))
    expect(exitWorker).not.toHaveBeenCalled()
    await act(async () => { fireEvent.click(screen.getByTestId('exit-confirm')) })
    expect(exitWorker).toHaveBeenCalledWith({ hostId: H, executionId: 'E1' })
  })

  describe('filter (worker test tab S4)', () => {
    const mixed = () => seed([
      row({ id: 'N1', session_id: 'SN', cwd: '/Users/w/proj', brief: 'normal one' }),
      row({ id: 'T1', session_id: 'ST', cwd: '/tmp/x', brief: 'test one' }),
      row({ id: 'T2', session_id: 'ST2', cwd: '/private/tmp/a/b', brief: 'test two' }),
    ])
    it('normal drops test cwds', () => {
      mixed()
      renderRows(vi.fn(), 'normal')
      expect(screen.getAllByTestId('executions-row')).toHaveLength(1)
      expect(screen.getByText('normal one')).toBeInTheDocument()
    })
    it('test keeps only test cwds', () => {
      mixed()
      renderRows(vi.fn(), 'test')
      expect(screen.getAllByTestId('executions-row')).toHaveLength(2)
      expect(screen.queryByText('normal one')).toBeNull()
    })
    it('no filter keeps every row (New Tab / activity list unchanged)', () => {
      mixed()
      renderRows()
      expect(screen.getAllByTestId('executions-row')).toHaveLength(3)
    })
    it('shows the empty copy when the filter leaves nothing', () => {
      seed([row({ id: 'T1', session_id: 'ST', cwd: '/tmp/x' })])
      renderRows(vi.fn(), 'normal')
      expect(screen.getByTestId(`${P}-empty`)).toBeInTheDocument()
    })
    it('a loading list whose live rows are all filtered out still shows the skeleton', () => {
      seed([row({ id: 'T1', session_id: 'ST', cwd: '/tmp/x' })], { phase: 'loading' })
      renderRows(vi.fn(), 'normal')
      expect(screen.getByTestId(`${P}-loading`)).toBeInTheDocument()
    })
  })

  describe('query / hideEmpty (worker test tab S5)', () => {
    const two = () => seed([
      row({ id: 'T1', session_id: 'ST', cwd: '/tmp/alpha', brief: 'one' }),
      row({ id: 'T2', session_id: 'ST2', cwd: '/tmp/beta', brief: 'two' }),
    ])
    it('query narrows the rows by cwd', () => {
      two()
      render(<HostWorkerRows hostId={H} onOpen={vi.fn()} testIdPrefix={P} filter="test" query="alpha" />)
      expect(screen.getAllByTestId('executions-row')).toHaveLength(1)
      expect(screen.getByText('one')).toBeInTheDocument()
    })
    it('query finds a handoff row by its shown title only with the host capability (#1771)', () => {
      useNexHostStore.setState({ byHost: { [H]: { ...readyEntry, capabilities: { session_title: { sources: ['ai'], max_bytes: 200 } } as never } } })
      seed([row({ id: 'T1', session_id: 'ST', cwd: '/tmp/repo', brief: '', session_title: { text: 'Zebrafinch', source: 'ai' } })])
      const { unmount } = render(<HostWorkerRows hostId={H} onOpen={vi.fn()} testIdPrefix={P} filter="test" query="zebrafinch" />)
      expect(screen.getAllByTestId('executions-row')).toHaveLength(1)
      unmount()
      useNexHostStore.setState({ byHost: { [H]: readyEntry } })
      render(<HostWorkerRows hostId={H} onOpen={vi.fn()} testIdPrefix={P} filter="test" query="zebrafinch" />)
      expect(screen.queryByTestId('executions-row')).toBeNull()
    })
    it('hideEmpty drops the empty copy', () => {
      two()
      render(<HostWorkerRows hostId={H} onOpen={vi.fn()} testIdPrefix={P} filter="test" query="nomatch" hideEmpty />)
      expect(screen.queryByTestId(`${P}-empty`)).toBeNull()
      expect(screen.queryByTestId('executions-row')).toBeNull()
    })
  })
})
