// spa/src/lib/nex/relative-age.test.ts
import { describe, it, expect } from 'vitest'
import { relativeAge } from './relative-age'

const SEC = 1_000
const MIN = 60 * SEC
const HOUR = 60 * MIN
const DAY = 24 * HOUR

describe('relativeAge', () => {
  const now = 10 * DAY

  it('is just_now under 60 seconds', () => {
    expect(relativeAge(now, now)).toEqual({ key: 'just_now', n: 0 })
    expect(relativeAge(now - 59 * SEC, now)).toEqual({ key: 'just_now', n: 59 })
  })

  it('switches to minutes at exactly 60 seconds', () => {
    expect(relativeAge(now - 60 * SEC, now)).toEqual({ key: 'minutes', n: 1 })
    expect(relativeAge(now - 59 * MIN - 59 * SEC, now)).toEqual({ key: 'minutes', n: 59 })
  })

  it('switches to hours at exactly 60 minutes', () => {
    expect(relativeAge(now - 60 * MIN, now)).toEqual({ key: 'hours', n: 1 })
    expect(relativeAge(now - 23 * HOUR - 59 * MIN, now)).toEqual({ key: 'hours', n: 23 })
  })

  it('switches to days at exactly 24 hours', () => {
    expect(relativeAge(now - 24 * HOUR, now)).toEqual({ key: 'days', n: 1 })
    expect(relativeAge(now - 3 * DAY - 5 * HOUR, now)).toEqual({ key: 'days', n: 3 })
  })

  it('treats a future timestamp as just_now', () => {
    expect(relativeAge(now + 5 * MIN, now)).toEqual({ key: 'just_now', n: 0 })
  })
})
