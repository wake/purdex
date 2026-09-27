// spa/src/lib/nex/activity.test.ts
import { describe, it, expect } from 'vitest'
import { normalizePhase } from './activity'

describe('normalizePhase', () => {
  it.each(['queued', 'starting', 'model', 'tool', 'idle', 'ended'] as const)('keeps the known phase %s', (p) => {
    expect(normalizePhase(p)).toBe(p)
  })

  it('an unknown phase is model (open set, contract §0 worker_rollup)', () => {
    expect(normalizePhase('awaiting_input')).toBe('model')
    expect(normalizePhase('something_new')).toBe('model')
    expect(normalizePhase('')).toBe('model')
  })

  it('a non-string is model too', () => {
    expect(normalizePhase(undefined)).toBe('model')
    expect(normalizePhase(3)).toBe('model')
  })
})
