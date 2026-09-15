import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import ExecutionHeader from './ExecutionHeader'
import type { ExecutionSummary } from '../../lib/nex/types'

const summary = (extra: Partial<ExecutionSummary> = {}): ExecutionSummary => ({
  id: 'exc_1',
  state: 'idle',
  provider: 'claude',
  principal_id: 'p',
  cwd: '/Users/w/repo',
  mount_kind: 'dev',
  brief: 'b',
  labels: {},
  created_at: 0,
  updated_at: 0,
  duration_ms: null,
  event_count: 0,
  observers: 2,
  archived: false,
  effective_profile: 'standard',
  turn_count: 3,
  ...extra,
})

const baseProps = {
  costUsd: 0,
  sse: 'open' as const,
  isMine: () => false,
  onInterrupt: vi.fn(),
  onTerminate: vi.fn(),
  busy: false,
}

describe('ExecutionHeader', () => {
  it('renders state dot text, profile, cwd basename, observers, and turns', () => {
    render(<ExecutionHeader {...baseProps} summary={summary()} />)
    expect(screen.getByTestId('execution-state')).toHaveTextContent('idle')
    expect(screen.getByText(/standard/)).toBeInTheDocument()
    expect(screen.getByText('repo')).toBeInTheDocument()
    expect(screen.getByText(/2 observers/i)).toBeInTheDocument()
    expect(screen.getByText(/3 turns/i)).toBeInTheDocument()
  })

  it('renders lease line: "(you)" when isMine, raw principal otherwise, "no lease" when absent', () => {
    const { rerender } = render(
      <ExecutionHeader {...baseProps} summary={summary({ lease: { principal_id: 'pdx:mlab/t-me', expires_at: 1 } })} isMine={() => true} />,
    )
    expect(screen.getByTestId('execution-lease')).toHaveTextContent('(you)')

    rerender(
      <ExecutionHeader {...baseProps} summary={summary({ lease: { principal_id: 'pdx:mlab/t-other', expires_at: 1 } })} isMine={() => false} />,
    )
    expect(screen.getByTestId('execution-lease')).toHaveTextContent('pdx:mlab/t-other')

    rerender(<ExecutionHeader {...baseProps} summary={summary()} isMine={() => false} />)
    expect(screen.getByTestId('execution-lease')).toHaveTextContent(/no lease/i)
  })

  it('SSE badge text follows sse', () => {
    const { rerender } = render(<ExecutionHeader {...baseProps} summary={summary()} sse="idle" />)
    expect(screen.getByTestId('execution-sse')).toHaveTextContent(/connecting/i)

    rerender(<ExecutionHeader {...baseProps} summary={summary()} sse="open" />)
    expect(screen.getByTestId('execution-sse')).toHaveTextContent(/live/i)

    rerender(<ExecutionHeader {...baseProps} summary={summary()} sse="paused" />)
    expect(screen.getByTestId('execution-sse')).toHaveTextContent(/paused/i)
  })
})
