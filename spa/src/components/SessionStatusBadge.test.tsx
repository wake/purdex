// spa/src/components/SessionStatusBadge.test.tsx
import { describe, it, expect } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import SessionStatusBadge from './SessionStatusBadge'
import type { AgentStatus } from '../stores/useAgentStore'

describe('SessionStatusBadge', () => {
  const cases: [AgentStatus, string][] = [
    ['running', 'bg-green-400'],
    ['waiting', 'bg-yellow-400'],
    ['idle', 'bg-gray-500'],
  ]

  cases.forEach(([status, expectedClass]) => {
    it(`renders ${expectedClass} for status "${status}"`, () => {
      cleanup()
      render(<SessionStatusBadge status={status} />)
      const badge = screen.getByTestId('status-badge')
      expect(badge.className).toContain(expectedClass)
      expect(badge).toHaveAttribute('title', status)
    })
  })

  it('renders as a small dot (w-2 h-2 rounded-full)', () => {
    cleanup()
    render(<SessionStatusBadge status="running" />)
    const badge = screen.getByTestId('status-badge')
    expect(badge.className).toContain('w-2')
    expect(badge.className).toContain('h-2')
    expect(badge.className).toContain('rounded-full')
  })

  it('returns null when status is undefined', () => {
    cleanup()
    const { container } = render(<SessionStatusBadge status={undefined} />)
    expect(container.innerHTML).toBe('')
  })
})
