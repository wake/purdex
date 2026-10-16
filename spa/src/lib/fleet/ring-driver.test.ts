import { describe, it, expect } from 'vitest'
import { ringDriver } from './ring-driver'

const base = { cpu: 10, mem: 10, level: 0, page_rate: 0, disk: 10 }

describe('ringDriver — the reading that sets the ring length', () => {
  it('cpu', () => expect(ringDriver({ ...base, cpu: 60 })).toBe('cpu'))
  it('memory', () => expect(ringDriver({ ...base, mem: 80, cpu: 20 })).toBe('mem'))
  it('pressure level score (2 -> 75)', () => expect(ringDriver({ ...base, level: 2, cpu: 20, mem: 50 })).toBe('pressure'))
  it('paging, capped at 100', () => expect(ringDriver({ ...base, page_rate: 900, cpu: 90, mem: 90 })).toBe('paging'))
  it('disk only from 85 %', () => {
    expect(ringDriver({ ...base, disk: 84, cpu: 5, mem: 5 })).toBe('cpu')
    expect(ringDriver({ ...base, disk: 90, cpu: 5, mem: 5 })).toBe('disk')
  })
  it('a tie goes to the first in order', () => {
    expect(ringDriver({ ...base, cpu: 75, level: 2 })).toBe('cpu')
    expect(ringDriver({ ...base, mem: 75, level: 2 })).toBe('mem')
    expect(ringDriver({ ...base, page_rate: 100, disk: 100, cpu: 0, mem: 0 })).toBe('paging')
  })
  it('the page rate is capped at 100, so cpu 100 beats a rate of 900 (tie to the first)', () => {
    expect(ringDriver({ ...base, cpu: 100, page_rate: 900 })).toBe('cpu')
  })
  it('level 1 scores 30; unknown level 0 scores nothing', () => {
    expect(ringDriver({ ...base, level: 1, cpu: 29, mem: 29 })).toBe('pressure')
    expect(ringDriver({ ...base, level: 0, cpu: 0, mem: 0 })).toBeNull()
  })
  it('everything 0 (or unknown): no driver, the ring is empty', () => {
    expect(ringDriver({ cpu: 0, mem: 0, level: 0, page_rate: 0, disk: 0 })).toBeNull()
  })
})
