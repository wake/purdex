import { describe, it, expect } from 'vitest'
import { pickWorkingVerb, formatWorkingElapsed, WORKING_VERBS } from './working-line'

describe('pickWorkingVerb', () => {
  it('is deterministic per turn key and drawn from the list', () => {
    expect(pickWorkingVerb('exc_1:3')).toBe(pickWorkingVerb('exc_1:3'))
    expect(WORKING_VERBS).toContain(pickWorkingVerb('exc_1:3'))
  })
  it('varies across turns', () => {
    const picked = new Set(Array.from({ length: 40 }, (_, i) => pickWorkingVerb(`exc_1:${i}`)))
    expect(picked.size).toBeGreaterThan(3)
  })
})

describe('formatWorkingElapsed', () => {
  it.each([
    [0, '0s'], [999, '0s'], [7_400, '7s'], [59_999, '59s'], [100_000, '1m 40s'], [3_600_000, '1h 00m'], [3_725_000, '1h 02m'],
    [-5, '0s'], [Number.NaN, '0s'],
  ])('%s ms -> %s', (ms, out) => { expect(formatWorkingElapsed(ms)).toBe(out) })
})
