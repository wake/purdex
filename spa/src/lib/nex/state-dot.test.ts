import { describe, it, expect } from 'vitest'
import { STATE_DOT_CLASSES, stateDotClass, workerDotClass } from './state-dot'

describe('workerDotClass (permission channel PC2: 「等待核准」 reuses the warning token)', () => {
  it('awaiting approval → the warning token, whatever the state', () => {
    expect(workerDotClass('running', true)).toBe('bg-status-warning')
  })
  it('not awaiting → the state colour, unchanged', () => {
    for (const state of Object.keys(STATE_DOT_CLASSES)) expect(workerDotClass(state, false)).toBe(stateDotClass(state))
    expect(workerDotClass('weird', false)).toBe('bg-text-muted')
  })
})

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
