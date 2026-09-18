import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
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

  // P-C.3b task 4 / exec-to-terminal spec §4.2: "Take to terminal" exists
  // only when the view passes `onTakeBack` (it decides from `from` / summary).
  it('renders no take-back control without onTakeBack', () => {
    render(<ExecutionHeader {...baseProps} summary={summary()} />)
    expect(screen.queryByTestId('take-back')).toBeNull()
  })

  it('renders the take-back control with onTakeBack, labelled and clickable', () => {
    const onTakeBack = vi.fn()
    render(<ExecutionHeader {...baseProps} summary={summary()} onTakeBack={onTakeBack} />)
    const btn = screen.getByTestId('take-back') as HTMLButtonElement
    expect(btn).toHaveTextContent(/take to terminal/i)
    expect(btn.disabled).toBe(false)
    fireEvent.click(btn)
    expect(onTakeBack).toHaveBeenCalledTimes(1)
  })

  it('disables the take-back control while takeBackBusy, independent of `busy`', () => {
    const onTakeBack = vi.fn()
    const { rerender } = render(<ExecutionHeader {...baseProps} summary={summary()} onTakeBack={onTakeBack} takeBackBusy />)
    const btn = screen.getByTestId('take-back') as HTMLButtonElement
    expect(btn.disabled).toBe(true)
    fireEvent.click(btn)
    expect(onTakeBack).not.toHaveBeenCalled()
    // `busy` (terminal execution) gates interrupt/terminate, not take-back:
    // an ended execution can still go back to its terminal.
    rerender(<ExecutionHeader {...baseProps} summary={summary({ state: 'failed' })} busy onTakeBack={onTakeBack} />)
    expect((screen.getByTestId('take-back') as HTMLButtonElement).disabled).toBe(false)
  })
})
