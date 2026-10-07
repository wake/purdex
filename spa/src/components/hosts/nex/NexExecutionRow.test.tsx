import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import NexExecutionRow from './NexExecutionRow'
import { STATE_DOT_CLASSES } from '../../../lib/nex/state-dot'
import type { ExecutionSummary } from '../../../lib/nex/types'

const row = (state: string): ExecutionSummary => ({
  id: 'exc_0123456789abcdef', state, provider: 'claude', principal_id: 'p', cwd: '/w/repo', mount_kind: 'dev', brief: 'b',
  labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false,
}) as ExecutionSummary

const renderRow = (state: string) => render(
  <table><tbody>
    <NexExecutionRow row={row(state)} confirmingTerminate={false} pending={false}
      onTerminateClick={vi.fn()} onTerminateConfirm={vi.fn()} onArchiveToggle={vi.fn()} />
  </tbody></table>,
)

describe('NexExecutionRow state dot (D8)', () => {
  it.each(Object.entries(STATE_DOT_CLASSES))('%s state renders its dot with %s', (state, cls) => {
    renderRow(state)
    const dot = screen.getByTitle(state)
    expect(dot).toHaveClass('rounded-full', cls)
  })

  it('an unknown state falls back to the muted dot', () => {
    renderRow('weird')
    expect(screen.getByTitle('weird')).toHaveClass('bg-text-muted')
  })

  it('shows the state label next to the dot', () => {
    renderRow('running')
    expect(screen.getByText('running')).toBeInTheDocument()
  })
})
