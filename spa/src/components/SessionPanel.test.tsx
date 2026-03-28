// spa/src/components/SessionPanel.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import SessionPanel from './SessionPanel'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'

vi.mock('../lib/api', () => ({
  listSessions: vi.fn().mockResolvedValue([]),
}))

beforeEach(() => {
  cleanup()
  useSessionStore.setState({ sessions: [], activeId: null })
  useAgentStore.setState({ statuses: {}, events: {}, unread: {}, focusedSession: null })
})

describe('SessionPanel', () => {
  it('shows empty state', () => {
    render(<SessionPanel />)
    expect(screen.getByText('No sessions')).toBeInTheDocument()
  })

  it('renders session list', () => {
    useSessionStore.setState({
      sessions: [
        { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'term', cc_session_id: '', cc_model: '', has_relay: false },
        { code: 'abc002', name: 'prod', cwd: '/tmp', mode: 'stream', cc_session_id: '', cc_model: '', has_relay: false },
      ],
      activeId: null,
    })
    render(<SessionPanel />)
    expect(screen.getByText('dev')).toBeInTheDocument()
    expect(screen.getByText('prod')).toBeInTheDocument()
  })

  it('highlights active session', () => {
    useSessionStore.setState({
      sessions: [
        { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'term', cc_session_id: '', cc_model: '', has_relay: false },
      ],
      activeId: 'abc001',
    })
    render(<SessionPanel />)
    const btn = screen.getByRole('button', { name: /dev/i })
    expect(btn.className).toContain('bg-surface-secondary')
  })

  it('sets active on click', () => {
    const setActive = vi.fn()
    useSessionStore.setState({
      sessions: [
        { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'term', cc_session_id: '', cc_model: '', has_relay: false },
      ],
      activeId: null,
      setActive,
    })
    render(<SessionPanel />)
    fireEvent.click(screen.getByRole('button', { name: /dev/i }))
    expect(setActive).toHaveBeenCalledWith('abc001')
  })

  it('shows terminal icon for term mode', () => {
    useSessionStore.setState({
      sessions: [
        { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'term', cc_session_id: '', cc_model: '', has_relay: false },
      ],
      activeId: null,
    })
    render(<SessionPanel />)
    // Terminal icon should be present (Phosphor Terminal icon)
    expect(screen.getByTestId('session-icon-abc001')).toBeInTheDocument()
  })

  it('shows agent status badge when agent is active', () => {
    useSessionStore.setState({
      sessions: [
        { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'term', cc_session_id: '', cc_model: '', has_relay: false },
      ],
      activeId: null,
    })
    // Set agent status
    useAgentStore.setState({ statuses: { abc001: 'idle' } })
    render(<SessionPanel />)
    expect(screen.getByTestId('status-badge')).toHaveAttribute('title', 'idle')
  })

  it('shows no badge when no agent status exists for session', () => {
    useSessionStore.setState({
      sessions: [
        { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'stream', cc_session_id: '', cc_model: '', has_relay: false },
      ],
      activeId: null,
    })
    // No agent status set
    render(<SessionPanel />)
    expect(screen.queryByTestId('status-badge')).toBeNull()
  })
})
