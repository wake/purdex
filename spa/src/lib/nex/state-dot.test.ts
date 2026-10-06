import { describe, it, expect } from 'vitest'
import { STATE_DOT_CLASSES, stateDotClass } from './state-dot'

describe('state dot (D8: same colours as the terminal agent badge)', () => {
  it('maps every state', () => {
    expect(STATE_DOT_CLASSES).toEqual({
      running: 'bg-status-success', queued: 'bg-status-warning', idle: 'bg-text-muted',
      failed: 'bg-status-error', rejected: 'bg-status-error', terminated: 'bg-text-muted',
    })
  })
  it('falls back to muted for an unknown state', () => {
    expect(stateDotClass('weird')).toBe('bg-text-muted')
  })
})
