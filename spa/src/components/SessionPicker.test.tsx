// spa/src/components/SessionPicker.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { SessionPicker } from './SessionPicker'
import type { Session } from '../lib/host-api'

const mockSessions: Session[] = [
  { code: 'abc001', name: 'dev-server', mode: 'terminal', cwd: '/home' },
  // Legacy input: a pre-P-D.2 daemon still reports 'stream'; the P-D.3 test
  // below checks the row renders it as a plain terminal session.
  { code: 'def002', name: 'claude-code', mode: 'stream', cwd: '/home' },
]

beforeEach(() => cleanup())

describe('SessionPicker', () => {
  it('renders session list', () => {
    render(
      <SessionPicker
        sessions={mockSessions}
        existingTabSessionNames={[]}
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    expect(screen.getByText('dev-server')).toBeTruthy()
    expect(screen.getByText('claude-code')).toBeTruthy()
  })

  it('shows no mode text and the same icon for every row (P-D.3)', () => {
    render(
      <SessionPicker
        sessions={mockSessions}
        existingTabSessionNames={[]}
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    const devItem = screen.getByText('dev-server').closest('button')!
    const ccItem = screen.getByText('claude-code').closest('button')!
    expect(devItem.textContent).not.toContain('terminal')
    expect(ccItem.textContent).not.toContain('stream')
    expect(devItem.querySelector('svg')?.innerHTML).toBe(ccItem.querySelector('svg')?.innerHTML)
  })

  it('marks sessions that already have tabs', () => {
    render(
      <SessionPicker
        sessions={mockSessions}
        existingTabSessionNames={['dev-server']}
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    const devItem = screen.getByText('dev-server').closest('button')!
    expect(devItem.textContent).toContain('Already open')
  })

  it('calls onSelect with session info', () => {
    const onSelect = vi.fn()
    render(
      <SessionPicker
        sessions={mockSessions}
        existingTabSessionNames={[]}
        onSelect={onSelect}
        onClose={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByText('dev-server'))
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ name: 'dev-server', mode: 'terminal' }))
  })

  it('filters sessions by search text', () => {
    render(
      <SessionPicker
        sessions={mockSessions}
        existingTabSessionNames={[]}
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    const input = screen.getByPlaceholderText('Search sessions...')
    fireEvent.change(input, { target: { value: 'claude' } })
    expect(screen.queryByText('dev-server')).toBeNull()
    expect(screen.getByText('claude-code')).toBeTruthy()
  })
})
