// Settings → Worker → Exited with the real history hook (WorkerExitedTab.test.tsx
// mocks it): a retry after a failed refresh keeps the rows it had, so the
// loading feedback must not depend on the list being empty (PR #1630 A1).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'

const listAll = vi.fn()
vi.mock('../../lib/nex/list-all-executions', () => ({ listAllExecutions: (...a: unknown[]) => listAll(...a), LIST_PAGE_LIMIT: 500, LIST_MAX_PAGES: 20 }))
vi.mock('../../hooks/useHostExecutions', () => ({
  useHostExecutions: () => ({ items: [], phase: 'ready', error: null, truncated: false, refetch: () => {}, refreshRevision: 0 }),
}))
import { WorkerExitedTab } from './WorkerExitedTab'
import { HISTORY_MIN_INTERVAL_MS } from '../../hooks/useExecutionHistory'
import { useExecutionListStore } from '../../stores/useExecutionListStore'
import type { ExecutionSummary } from '../../lib/nex/types'

const r = (o: Partial<ExecutionSummary>): ExecutionSummary => ({
  id: 'x', state: 'terminated', provider: 'cc', principal_id: 'p', cwd: '/w/proj', mount_kind: 'none', brief: '', labels: {},
  created_at: 1, updated_at: Date.now(), duration_ms: null, event_count: 0, observers: 0, archived: true, ...o,
})
const res = (items: ExecutionSummary[]) => ({ items, dropped: 0, truncated: false, stuck: false, stuckPage: null })
/** The shared live list's refresh: what tells the history hook to walk again. */
const bump = (n: number) => act(() => {
  useExecutionListStore.setState({ byHost: { h1: { items: [], phase: 'ready', error: null, lastSeq: null, refreshRevision: n, truncated: false } as never } })
})
const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0) })

describe('WorkerExitedTab retry with rows kept', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    listAll.mockReset()
    useExecutionListStore.setState({ byHost: {} })
  })
  afterEach(() => {
    vi.useRealTimers()
    useExecutionListStore.setState({ byHost: {} })
  })

  it('a retry after a failed refresh shows the loading line and aria-busy over the rows it keeps', async () => {
    listAll.mockResolvedValueOnce(res([r({ id: 'e1', session_id: 'S1', brief: 'one' })]))
    render(<WorkerExitedTab hostId="h1" />)
    await flush()
    expect(screen.getAllByTestId('worker-exited-row')).toHaveLength(1)

    // A later refresh fails: the rows stay, the error shows.
    listAll.mockRejectedValueOnce(new Error('down'))
    bump(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(HISTORY_MIN_INTERVAL_MS) })
    expect(screen.getByTestId('worker-exited-error')).toBeInTheDocument()
    expect(screen.getAllByTestId('worker-exited-row')).toHaveLength(1)

    // Retry, with its walk pending.
    let answer!: (v: unknown) => void
    listAll.mockReturnValueOnce(new Promise((resolve) => { answer = resolve }))
    fireEvent.click(screen.getByTestId('worker-exited-retry'))
    expect(listAll).toHaveBeenCalledTimes(3)
    expect(screen.queryByTestId('worker-exited-error')).toBeNull()
    expect(screen.getByTestId('worker-exited-loading')).toBeInTheDocument()
    expect(screen.getByRole('list')).toHaveAttribute('aria-busy', 'true')
    expect(screen.getAllByTestId('worker-exited-row')).toHaveLength(1)

    await act(async () => { answer(res([r({ id: 'e1', session_id: 'S1', brief: 'one' }), r({ id: 'e2', session_id: 'S2', brief: 'two' })])) })
    await flush()
    expect(screen.queryByTestId('worker-exited-loading')).toBeNull()
    expect(screen.getByRole('list')).not.toHaveAttribute('aria-busy')
    expect(screen.getAllByTestId('worker-exited-row')).toHaveLength(2)
  })

  it('a background refresh over the rows shows no loading line (as today)', async () => {
    listAll.mockResolvedValueOnce(res([r({ id: 'e1', session_id: 'S1', brief: 'one' })]))
    render(<WorkerExitedTab hostId="h1" />)
    await flush()
    let answer!: (v: unknown) => void
    listAll.mockReturnValueOnce(new Promise((resolve) => { answer = resolve }))
    bump(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(HISTORY_MIN_INTERVAL_MS) })
    expect(listAll).toHaveBeenCalledTimes(2)
    expect(screen.queryByTestId('worker-exited-loading')).toBeNull()
    expect(screen.getByRole('list')).not.toHaveAttribute('aria-busy')
    await act(async () => { answer(res([r({ id: 'e1', session_id: 'S1', brief: 'one' })])) })
  })
})
