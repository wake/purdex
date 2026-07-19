import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { ExecutionDetailPage } from './ExecutionDetailPage'
import { fetchExecutionView, type ExecutionView } from '../lib/execution-api'
import { useTabStore } from '../stores/useTabStore'
import { createTab } from '../types/tab'

vi.mock('../lib/execution-api', async () => {
  const actual = await vi.importActual<typeof import('../lib/execution-api')>('../lib/execution-api')
  return {
    ...actual,
    fetchExecutionView: vi.fn(),
    resolveExecutionHostId: vi.fn(() => 'host-a'),
  }
})

const mockFetch = vi.mocked(fetchExecutionView)

function view(overrides: Partial<ExecutionView> = {}): ExecutionView {
  return {
    execution_id: 'exc_1',
    dispatch_id: 'dsp_1',
    status: 'running',
    launch_state: 'launched',
    session_code: null,
    session_name: 'pdx-exec-1',
    provider: 'claude',
    attempt_no: 1,
    repo_location: '/repo/a',
    head_at_start: 'base123',
    dirty_at_start: false,
    sandbox_profile: 'workspace-write',
    created_at: 0,
    updated_at: 0,
    ...overrides,
  }
}

beforeEach(() => {
  mockFetch.mockReset()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
})
afterEach(cleanup)

describe('ExecutionDetailPage', () => {
  it('shows a loading state, then the projection', async () => {
    mockFetch.mockResolvedValue(view({ status: 'accepted' }))
    render(<ExecutionDetailPage executionId="exc_1" />)
    expect(screen.getByTestId('execution-loading')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('execution-detail')).toBeInTheDocument())
    // accepted → surfaced as Queued
    expect(screen.getByTestId('execution-status')).toHaveTextContent('Queued')
    expect(screen.getByText('dsp_1')).toBeInTheDocument()
  })

  it('renders each terminal status label + diff summary', async () => {
    mockFetch.mockResolvedValue(
      view({
        status: 'completed',
        outcome_source: 'result',
        artifacts: [{ kind: 'diff', pointer: 'pdx://h/execution/exc_1/diff', meta: { files: 3, add: 12, del: 4 } }],
      }),
    )
    render(<ExecutionDetailPage executionId="exc_1" />)
    await waitFor(() => expect(screen.getByTestId('execution-status')).toHaveTextContent('Completed'))
    const diff = screen.getByTestId('execution-diff')
    expect(diff).toHaveTextContent('3 files')
    expect(diff).toHaveTextContent('+12')
    expect(diff).toHaveTextContent('−4')
  })

  it('renders a stable not-found landing on 404 (null)', async () => {
    mockFetch.mockResolvedValue(null)
    render(<ExecutionDetailPage executionId="exc_gone" />)
    await waitFor(() => expect(screen.getByTestId('execution-not-found')).toBeInTheDocument())
  })

  it('renders an error landing (no dead-end) on fetch failure', async () => {
    mockFetch.mockRejectedValue(new Error('boom'))
    render(<ExecutionDetailPage executionId="exc_1" />)
    await waitFor(() => expect(screen.getByTestId('execution-error')).toBeInTheDocument())
  })

  it('is observe-only: never renders any text input', async () => {
    mockFetch.mockResolvedValue(view({ session_code: 'sess1', status: 'running' }))
    const { container } = render(<ExecutionDetailPage executionId="exc_1" />)
    await waitFor(() => expect(screen.getByTestId('execution-detail')).toBeInTheDocument())
    expect(container.querySelector('input')).toBeNull()
    expect(container.querySelector('textarea')).toBeNull()
  })

  it('offers the observe affordance only when a matching session tab is open', async () => {
    mockFetch.mockResolvedValue(view({ session_code: 'sess1', status: 'running' }))
    // No tab open yet → no observe button.
    const { rerender } = render(<ExecutionDetailPage executionId="exc_1" />)
    await waitFor(() => expect(screen.getByTestId('execution-detail')).toBeInTheDocument())
    expect(screen.queryByTestId('execution-observe')).toBeNull()

    // Open a session tab for sess1 → affordance appears (focus-only, no stdin).
    const tab = createTab({
      kind: 'tmux-session', hostId: 'host-a', sessionCode: 'sess1',
      mode: 'stream', cachedName: 's', tmuxInstance: '',
    })
    useTabStore.getState().addTab(tab)
    rerender(<ExecutionDetailPage executionId="exc_1" />)
    await waitFor(() => expect(screen.getByTestId('execution-observe')).toBeInTheDocument())
  })
})
