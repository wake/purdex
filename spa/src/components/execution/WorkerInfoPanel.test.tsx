import { describe, it, expect, vi } from 'vitest'
import { createRef } from 'react'
import { render, screen, fireEvent } from '@testing-library/react'
import WorkerInfoPanel from './WorkerInfoPanel'
import type { ExecutionSummary } from '../../lib/nex/types'

const summary = (extra: Partial<ExecutionSummary> = {}): ExecutionSummary => ({
  id: 'exc_1', state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/Users/w/Workspace/repo', mount_kind: 'dev',
  brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false,
  effective_profile: 'standard', ...extra,
})

const renderPanel = (s: ExecutionSummary, onClose = vi.fn()) => {
  const anchorRef = createRef<HTMLElement>()
  render(<WorkerInfoPanel summary={s} anchorRef={anchorRef} onClose={onClose} />)
  return onClose
}

describe('WorkerInfoPanel', () => {
  it('shows the provider, profile and full cwd', () => {
    renderPanel(summary({ requested_profile: 'fast', effective_profile: 'standard' }))
    expect(screen.getByTestId('worker-info-provider')).toHaveTextContent('claude')
    // Requested and effective differ → both are shown.
    const profile = screen.getByTestId('worker-info-profile')
    expect(profile).toHaveTextContent('fast')
    expect(profile).toHaveTextContent('standard')
    const cwd = screen.getByTestId('worker-info-cwd')
    expect(cwd).toHaveTextContent('/Users/w/Workspace/repo')
    expect(cwd.className).toMatch(/\bselect-text\b/)
    expect(screen.queryByTestId('worker-info-archived')).toBeNull()
  })

  it('shows the requested profile when the effective one is not known yet', () => {
    renderPanel(summary({ effective_profile: undefined, requested_profile: 'fast' }))
    expect(screen.getByTestId('worker-info-profile')).toHaveTextContent(/^fast$/)
  })

  it('hides the session row when there is none', () => {
    renderPanel(summary())
    expect(screen.queryByTestId('worker-info-session')).toBeNull()
  })

  it('shows the session id when there is one', () => {
    renderPanel(summary({ session_id: 'sess-123', archived: true }))
    expect(screen.getByTestId('worker-info-session')).toHaveTextContent('sess-123')
    expect(screen.getByTestId('worker-info-archived')).toBeInTheDocument()
  })

  it('falls back to the resume session id', () => {
    renderPanel(summary({ resume_session_id: 'sess-r' }))
    expect(screen.getByTestId('worker-info-session')).toHaveTextContent('sess-r')
  })

  it('closes on Escape', () => {
    const onClose = renderPanel(summary())
    expect(screen.getByTestId('worker-info-panel')).toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})
