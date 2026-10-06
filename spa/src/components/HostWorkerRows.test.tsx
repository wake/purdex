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

const renderRows = (onOpen = vi.fn()) => render(<HostWorkerRows hostId={H} onOpen={onOpen} testIdPrefix={P} />)

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

  it('shows the truncation notice', () => {
    seed([row({ id: 'E1' })], { truncated: true })
    renderRows()
    expect(screen.getByTestId(`${P}-truncated`)).toBeInTheDocument()
  })

  it('exits an idle row at once', async () => {
    seed([row({ id: 'E1', state: 'idle' })])
    renderRows()
    await act(async () => { fireEvent.click(screen.getByTestId('executions-row-exit')) })
    expect(exitWorker).toHaveBeenCalledWith({ hostId: H, executionId: 'E1' })
  })

  it('asks before exiting a running row', async () => {
    seed([row({ id: 'E1', state: 'running' })])
    renderRows()
    fireEvent.click(screen.getByTestId('executions-row-exit'))
    expect(exitWorker).not.toHaveBeenCalled()
    await act(async () => { fireEvent.click(screen.getByTestId('exit-confirm')) })
    expect(exitWorker).toHaveBeenCalledWith({ hostId: H, executionId: 'E1' })
  })
})
